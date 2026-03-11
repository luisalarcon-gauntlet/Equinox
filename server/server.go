// Package server implements the HTTP API for Project Equinox.
// Handlers are thin: they validate input, call service interfaces, and encode
// the response. No business logic lives here.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/equinox/equivalence"
	equinoxerrors "github.com/equinox/errors"
	"github.com/equinox/logger"
	"github.com/equinox/models"
	"github.com/equinox/venues"
)


// VenueConnector re-exports venues.VenueConnector so callers only need to
// import the server package for dependency injection in tests.
type VenueConnector = venues.VenueConnector

// EquivalenceDetector is the interface the server uses to check market pairs.
// Defined here (not imported from equivalence/) so tests can inject a mock
// without depending on the live detector implementation.
type EquivalenceDetector interface {
	Detect(ctx context.Context, marketA, marketB models.Market) (models.MatchResult, error)
	// DetectAllPairs runs the full two-phase pipeline (heuristics → batched AI)
	// on a pre-filtered slice of candidate pairs and returns results in the same order.
	DetectAllPairs(ctx context.Context, pairs []models.MarketPair) ([]models.MatchResult, error)
}

// Router is the interface the server uses to produce routing decisions.
type Router interface {
	Route(match models.MatchResult, side string, size float64) (models.RoutingDecision, error)
}

// Server is the HTTP server for Equinox.
// It holds no mutable state beyond the match cache, which is the only piece
// of inter-request state in this prototype (no database, no session store).
type Server struct {
	connectors []venues.VenueConnector
	detector   EquivalenceDetector
	router     Router
	log        *logger.Logger
	mux        *http.ServeMux
	staticFS   fs.FS

	// matchCache is populated by /search and consulted by /route.
	// Keyed by both MarketA.ID and MarketB.ID so either market's ID is a valid
	// route request handle.
	matchMu    sync.RWMutex
	matchCache map[string]models.MatchResult
}

// NewServer constructs and registers all routes on a new Server.
//
//   - staticFS: an fs.FS whose root contains index.html (use fs.Sub to strip path prefix).
//   - connectors: one entry per venue; fetched in parallel on /search.
//   - detector: equivalence detector (heuristic + AI hybrid).
//   - router: routing engine that scores venues for a hypothetical order.
//   - log: structured logger.
func NewServer(
	staticFS fs.FS,
	connectors []venues.VenueConnector,
	detector EquivalenceDetector,
	router Router,
	log *logger.Logger,
) *Server {
	s := &Server{
		connectors: connectors,
		detector:   detector,
		router:     router,
		log:        log,
		mux:        http.NewServeMux(),
		staticFS:   staticFS,
		matchCache: make(map[string]models.MatchResult),
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/search", s.handleSearch)
	s.mux.HandleFunc("/route", s.handleRoute)
	s.mux.HandleFunc("/", s.handleRoot)
}

// ServeHTTP implements http.Handler, making Server usable with httptest.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Start starts listening on addr (e.g. ":8080") and blocks until error.
func (s *Server) Start(addr string) error {
	s.log.Info("server", "", fmt.Sprintf("listening on %s", addr))
	if err := http.ListenAndServe(addr, s.mux); err != nil {
		return &equinoxerrors.EquinoxError{
			Layer:   "server",
			Message: "HTTP server failed",
			Err:     err,
		}
	}
	return nil
}

// ── Handlers ──────────────────────────────────────────────────────────────────

// handleHealth returns {"status":"ok"} for uptime monitoring and the UI health indicator.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// routeRequest is the shape of the POST /route body.
type routeRequest struct {
	MarketID string  `json:"market_id"`
	Side     string  `json:"side"`
	Size     float64 `json:"size"`
}

// handleRoute looks up a cached MatchResult and calls the routing engine.
//
// The match cache is populated by /search. Calling /route without a prior
// /search returns 404. This is intentional — the prototype requires search
// context to route (the full pair of canonical markets must be available).
func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req routeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Error("server", "", "failed to decode route request", err)
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.MarketID == "" {
		writeError(w, http.StatusBadRequest, "market_id is required")
		return
	}
	if req.Side == "" {
		writeError(w, http.StatusBadRequest, "side is required (yes or no)")
		return
	}
	if req.Size <= 0 {
		writeError(w, http.StatusBadRequest, "size must be a positive dollar amount")
		return
	}

	s.matchMu.RLock()
	match, ok := s.matchCache[req.MarketID]
	s.matchMu.RUnlock()

	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf(
			"market %q not found in cache — run /search first to populate available matches",
			req.MarketID,
		))
		return
	}

	decision, err := s.router.Route(match, req.Side, req.Size)
	if err != nil {
		s.log.Error("server", "", "routing engine failed", err)
		writeError(w, http.StatusInternalServerError, "routing failed: "+err.Error())
		return
	}

	s.log.Info("server", "", fmt.Sprintf(
		"route $%.0f %s on market %q → %s (confidence %.2f)",
		req.Size, req.Side, req.MarketID, decision.RecommendedVenue, decision.Confidence,
	))

	writeJSON(w, http.StatusOK, sanitizeRoutingDecision(decision))
}

// handleSearch fetches markets from all venues in parallel, runs equivalence
// detection on all cross-venue pairs, caches the results, and returns a
// SearchResponse payload.
//
// When no pair clears the equivalence threshold the response has
// NoMatchesAboveThreshold=true and the top-3 raw results from each venue
// are included as Suggestions so the UI can ask the user to refine.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "q parameter is required")
		return
	}

	ctx := r.Context()

	// Fetch from all connectors in parallel.
	type venueResult struct {
		venue   string
		markets []models.Market
		err     error
	}

	ch := make(chan venueResult, len(s.connectors))
	for _, c := range s.connectors {
		go func(conn venues.VenueConnector) {
			markets, err := conn.FetchMarkets(ctx, query)
			ch <- venueResult{venue: conn.GetVenueName(), markets: markets, err: err}
		}(c)
	}

	venueMarkets := make(map[string][]models.Market)
	for range s.connectors {
		result := <-ch
		if result.err != nil {
			s.log.Error("server", result.venue, "fetch failed", result.err)
		} else {
			venueMarkets[result.venue] = result.markets
		}
	}

	kalshiMarkets := venueMarkets["kalshi"]
	polyMarkets := venueMarkets["polymarket"]

	if len(kalshiMarkets) == 0 || len(polyMarkets) == 0 {
		s.log.Warn("server", "", fmt.Sprintf(
			`search summary: query=%q kalshi=%d polymarket=%d matches=%d ai_calls=%d`,
			query, len(kalshiMarkets), len(polyMarkets), 0, 0,
		))
		resp := models.SearchResponse{
			Matches:                 []models.MatchResult{},
			NoMatchesAboveThreshold: true,
			Message:                 "No markets found for this query on one or both venues. Please try a different search term.",
			Suggestions: models.SearchSuggestions{
				Kalshi:     sanitizeMarkets(top3(kalshiMarkets)),
				Polymarket: sanitizeMarkets(top3(polyMarkets)),
			},
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}

	matches, confirmedMatches, aiCalls := s.detectAllPairs(ctx, kalshiMarkets, polyMarkets)

	// Refresh the cache: replace with results from this search.
	s.matchMu.Lock()
	s.matchCache = make(map[string]models.MatchResult)
	for _, m := range matches {
		s.matchCache[m.MarketA.ID] = m
		s.matchCache[m.MarketB.ID] = m
	}
	s.matchMu.Unlock()

	s.log.Info("server", "", fmt.Sprintf(
		`search summary: query=%q kalshi=%d polymarket=%d matches=%d ai_calls=%d`,
		query, len(kalshiMarkets), len(polyMarkets), confirmedMatches, aiCalls,
	))

	sanitized := make([]models.MatchResult, len(matches))
	for i, m := range matches {
		sanitized[i] = sanitizeMatchResult(m)
	}

	var resp models.SearchResponse
	if len(matches) == 0 {
		resp = models.SearchResponse{
			Matches:                 []models.MatchResult{},
			NoMatchesAboveThreshold: true,
			Message:                 "No confident cross-venue match found for this query. Try a more specific search term.",
			Suggestions: models.SearchSuggestions{
				Kalshi:     sanitizeMarkets(top3(kalshiMarkets)),
				Polymarket: sanitizeMarkets(top3(polyMarkets)),
			},
		}
	} else {
		resp = models.SearchResponse{
			Matches:     sanitized,
			Suggestions: models.SearchSuggestions{},
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// candidatePair returns true when the two markets should be evaluated for
// equivalence. We only reject pairs that provably cannot be equivalent:
//
//   - Both sides have parseable ResolutionDates AND they differ by more than
//     candidatePairDateWindow (30 days).
//   - Both sides have an Underlying asset AND the assets do not match.
//
// When either side lacks a ResolutionDate the date check is skipped entirely —
// the heuristic layer handles unknown dates by scoring them 0.0, which reduces
// overall confidence without hard-blocking the pair.
// candidatePair returns true when the two markets should be evaluated for
// equivalence. The only hard pre-filter is asset mismatch: when both markets
// name a specific underlying asset (e.g. "BTC", "SOL") and those assets differ,
// the markets are definitively unrelated.
//
// Date-based pre-filtering is intentionally absent. Kalshi's close_time field
// represents the end of the trading/settlement window (often the end of the
// calendar year for annual award markets), while Polymarket's endDate is the
// expected resolution night. These fields are semantically incomparable across
// venues, so comparing them produces false negatives (e.g., Kalshi Dec 31 vs
// Polymarket Mar 15 for the same Oscar market → 291-day gap → all pairs
// rejected). The heuristic entity scorer and AI layer handle equivalence
// without needing a date pre-filter.
func candidatePair(a, b models.Market) bool {
	// Underlying gate: only reject when both underlyings are known and differ.
	if a.Underlying != "" && b.Underlying != "" {
		if !strings.EqualFold(a.Underlying, b.Underlying) {
			return false
		}
	}
	return true
}

// detectAllPairs builds the cross-venue candidate set (underlying gate only),
// then delegates to DetectAllPairs which runs heuristics and batched AI calls.
//
// Only pairs where IsMatch=true are returned. When nothing clears the threshold
// the caller is responsible for surfacing the raw venue results as suggestions.
func (s *Server) detectAllPairs(ctx context.Context, kalshi, polymarket []models.Market) ([]models.MatchResult, int, int64) {
	aiStats := equivalence.NewAIStatsCollector()
	ctx = equivalence.WithAIStatsCollector(ctx, aiStats)

	var pairs []models.MarketPair
	for _, kMkt := range kalshi {
		for _, pMkt := range polymarket {
			if candidatePair(kMkt, pMkt) {
				pairs = append(pairs, models.MarketPair{A: kMkt, B: pMkt})
			}
		}
	}

	if len(pairs) == 0 {
		return nil, 0, 0
	}

	allResults, err := s.detector.DetectAllPairs(ctx, pairs)
	if err != nil {
		s.log.Error("server", "", "DetectAllPairs failed", err)
		return nil, 0, 0
	}

	var matches []models.MatchResult
	for _, r := range allResults {
		if r.IsMatch {
			matches = append(matches, r)
		}
	}

	return matches, len(matches), aiStats.Count()
}

// top3 returns the first n (up to 3) markets from the slice.
func top3(markets []models.Market) []models.Market {
	const n = 3
	if len(markets) <= n {
		return markets
	}
	return markets[:n]
}

// sanitizeMarkets strips RawData from a slice of markets.
func sanitizeMarkets(markets []models.Market) []models.Market {
	out := make([]models.Market, len(markets))
	for i, m := range markets {
		m.RawData = nil
		out[i] = m
	}
	return out
}

// handleRoot serves the embedded index.html for any request to "/".
// Unknown sub-paths return 404 so browsers don't silently receive HTML for
// mis-typed API URLs.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	content, err := fs.ReadFile(s.staticFS, "index.html")
	if err != nil {
		s.log.Error("server", "", "failed to read index.html from embedded FS", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// ── JSON helpers ──────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// ── Response sanitisation ─────────────────────────────────────────────────────

// sanitizeMatchResult strips RawData from both markets before JSON encoding.
// RawData preserves the original API response for internal auditability but
// is too large and venue-specific to expose in the public API.
func sanitizeMatchResult(m models.MatchResult) models.MatchResult {
	m.MarketA.RawData = nil
	m.MarketB.RawData = nil
	return m
}

// sanitizeRoutingDecision strips RawData from the recommended market.
func sanitizeRoutingDecision(d models.RoutingDecision) models.RoutingDecision {
	d.Market.RawData = nil
	return d
}
