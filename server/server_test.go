package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/equinox/logger"
	"github.com/equinox/models"
	"github.com/equinox/server"
)

// ── Mock implementations ─────────────────────────────────────────────────────

type mockConnector struct {
	name    string
	markets []models.Market
	err     error
}

func (m *mockConnector) FetchMarkets(_ context.Context, _ string) ([]models.Market, error) {
	return m.markets, m.err
}
func (m *mockConnector) GetVenueName() string { return m.name }

type mockDetector struct {
	result models.MatchResult
	err    error
}

func (m *mockDetector) Detect(_ context.Context, a, b models.Market) (models.MatchResult, error) {
	r := m.result
	// Populate MarketA/B from the actual call so the cache key matches.
	r.MarketA = a
	r.MarketB = b
	return r, m.err
}

func (m *mockDetector) DetectAllPairs(_ context.Context, pairs []models.MarketPair) ([]models.MatchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	results := make([]models.MatchResult, len(pairs))
	for i, p := range pairs {
		r := m.result
		r.MarketA = p.A
		r.MarketB = p.B
		results[i] = r
	}
	return results, nil
}

type mockRouter struct {
	decision models.RoutingDecision
	err      error
}

func (m *mockRouter) Route(_ models.MatchResult, _ string, _ float64) (models.RoutingDecision, error) {
	return m.decision, m.err
}

// ── Helpers ───────────────────────────────────────────────────────────────────

var testFS = fstest.MapFS{
	"index.html": {Data: []byte("<html><body>Equinox</body></html>")},
}

func silentLogger() *logger.Logger {
	return logger.New(io.Discard)
}

// testMarket returns a market with ResolutionDate set so server candidatePair
// pre-filter considers it (same date required for equivalence). Tests that
// expect a match use the same date on both venues.
func testMarket(venue, id string) models.Market {
	return models.Market{
		ID:             id,
		VenueID:        "native-" + id,
		Venue:          venue,
		Title:          "will event happen?",
		YesBid:         0.48,
		YesAsk:         0.52,
		YesMid:         0.50,
		NoPrice:        0.50,
		Spread:         0.04,
		Liquidity:      100_000,
		ResolutionDate: "2026-03-15", // same date so candidatePair allows the pair
		FetchedAt:      time.Now(),
		Status:         "open",
		Category:       "politics",
	}
}

func testMatch(a, b models.Market) models.MatchResult {
	return models.MatchResult{
		MarketA:    a,
		MarketB:    b,
		IsMatch:    true,
		Confidence: 0.90,
		Method:     "heuristic",
		Reasoning:  "high entity overlap",
		MatchedAt:  time.Now(),
	}
}

func testDecision(venue string) models.RoutingDecision {
	return models.RoutingDecision{
		RecommendedVenue: venue,
		OrderSide:        "yes",
		OrderSize:        500,
		Confidence:       0.85,
		Reasoning:        "better price on " + venue,
		Warnings:         []string{},
		DecidedAt:        time.Now(),
	}
}

// ── /health ───────────────────────────────────────────────────────────────────

func TestHealthEndpointReturnsOK(t *testing.T) {
	srv := server.NewServer(testFS, nil, &mockDetector{}, &mockRouter{}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestHealthEndpointReturnsJSON(t *testing.T) {
	srv := server.NewServer(testFS, nil, &mockDetector{}, &mockRouter{}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
}

func TestHealthEndpointMethodNotAllowed(t *testing.T) {
	srv := server.NewServer(testFS, nil, &mockDetector{}, &mockRouter{}, silentLogger())

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// ── /search ───────────────────────────────────────────────────────────────────

func TestSearchEndpointRequiresQuery(t *testing.T) {
	srv := server.NewServer(testFS, nil, &mockDetector{}, &mockRouter{}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/search", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestSearchEndpointMethodNotAllowed(t *testing.T) {
	srv := server.NewServer(testFS, nil, &mockDetector{}, &mockRouter{}, silentLogger())

	req := httptest.NewRequest(http.MethodPost, "/search?q=test", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestSearchEndpointReturnsMatches(t *testing.T) {
	kMarket := testMarket("kalshi", "k-id-1")
	pMarket := testMarket("polymarket", "p-id-1")

	kalshiConn := &mockConnector{name: "kalshi", markets: []models.Market{kMarket}}
	polyConn := &mockConnector{name: "polymarket", markets: []models.Market{pMarket}}

	detector := &mockDetector{
		result: models.MatchResult{IsMatch: true, Confidence: 0.90, Method: "heuristic"},
	}

	srv := server.NewServer(
		testFS,
		[]server.VenueConnector{kalshiConn, polyConn},
		detector,
		&mockRouter{},
		silentLogger(),
	)

	req := httptest.NewRequest(http.MethodGet, "/search?q=election", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp models.SearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(resp.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(resp.Matches))
	}
	if !resp.Matches[0].IsMatch {
		t.Error("expected IsMatch=true")
	}
	if resp.NoMatchesAboveThreshold {
		t.Error("expected NoMatchesAboveThreshold=false when matches exist")
	}
}

func TestSearchEndpointSkipsConflictingUnderlyingPairs(t *testing.T) {
	// Markets with different known underlying assets (BTC vs SOL) must be
	// rejected by candidatePair — they are definitively unrelated.
	kMarket := testMarket("kalshi", "k-btc")
	kMarket.Underlying = "BTC"
	pMarket := testMarket("polymarket", "p-sol")
	pMarket.Underlying = "SOL" // different asset — should be rejected

	kalshiConn := &mockConnector{name: "kalshi", markets: []models.Market{kMarket}}
	polyConn := &mockConnector{name: "polymarket", markets: []models.Market{pMarket}}
	detector := &mockDetector{result: models.MatchResult{IsMatch: true, Confidence: 0.90, Method: "heuristic"}}

	srv := server.NewServer(
		testFS,
		[]server.VenueConnector{kalshiConn, polyConn},
		detector,
		&mockRouter{},
		silentLogger(),
	)

	req := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp models.SearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(resp.Matches) != 0 {
		t.Errorf("expected 0 matches (conflicting underlying assets → candidatePair rejects), got %d", len(resp.Matches))
	}
}

func TestSearchEndpointAllowsMissingDatePairs(t *testing.T) {
	// One side has no ResolutionDate (e.g. Polymarket search market with no
	// parseable endDate). The pair must NOT be pre-filtered — the heuristic
	// layer is responsible for scoring it (with a 0 date contribution).
	kMarket := testMarket("kalshi", "k-has-date")
	kMarket.ResolutionDate = "2026-03-15"
	pMarket := testMarket("polymarket", "p-no-date")
	pMarket.ResolutionDate = "" // simulates a missing endDate from Polymarket API

	kalshiConn := &mockConnector{name: "kalshi", markets: []models.Market{kMarket}}
	polyConn := &mockConnector{name: "polymarket", markets: []models.Market{pMarket}}
	detector := &mockDetector{result: models.MatchResult{IsMatch: true, Confidence: 0.80, Method: "heuristic"}}

	srv := server.NewServer(
		testFS,
		[]server.VenueConnector{kalshiConn, polyConn},
		detector,
		&mockRouter{},
		silentLogger(),
	)

	req := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp models.SearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(resp.Matches) != 1 {
		t.Errorf("expected 1 match (missing date on one side must not block the pair), got %d", len(resp.Matches))
	}
}

func TestSearchEndpointAllowsNearDatePairs(t *testing.T) {
	// Dates within the 30-day window (e.g. Kalshi closes 13 days after ceremony)
	// must reach the detector — the heuristic scores date proximity itself.
	kMarket := testMarket("kalshi", "k-near-date")
	kMarket.ResolutionDate = "2026-03-15"
	pMarket := testMarket("polymarket", "p-near-date")
	pMarket.ResolutionDate = "2026-03-02" // 13 days earlier — inside window

	kalshiConn := &mockConnector{name: "kalshi", markets: []models.Market{kMarket}}
	polyConn := &mockConnector{name: "polymarket", markets: []models.Market{pMarket}}
	detector := &mockDetector{result: models.MatchResult{IsMatch: true, Confidence: 0.85, Method: "heuristic"}}

	srv := server.NewServer(
		testFS,
		[]server.VenueConnector{kalshiConn, polyConn},
		detector,
		&mockRouter{},
		silentLogger(),
	)

	req := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp models.SearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(resp.Matches) != 1 {
		t.Errorf("expected 1 match (dates within 30-day window must reach detector), got %d", len(resp.Matches))
	}
}

func TestSearchEndpointNoMatchReturnsNoMatchFlag(t *testing.T) {
	kMarket := testMarket("kalshi", "k-id-1")
	pMarket := testMarket("polymarket", "p-id-1")

	kalshiConn := &mockConnector{name: "kalshi", markets: []models.Market{kMarket}}
	polyConn := &mockConnector{name: "polymarket", markets: []models.Market{pMarket}}

	// Detector says NOT above benchmark — expect no matches and suggestions.
	detector := &mockDetector{
		result: models.MatchResult{IsMatch: false, Confidence: 0.10, Method: "heuristic"},
	}

	srv := server.NewServer(
		testFS,
		[]server.VenueConnector{kalshiConn, polyConn},
		detector,
		&mockRouter{},
		silentLogger(),
	)

	req := httptest.NewRequest(http.MethodGet, "/search?q=unrelated", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp models.SearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if len(resp.Matches) != 0 {
		t.Errorf("expected 0 matches below benchmark, got %d", len(resp.Matches))
	}
	if !resp.NoMatchesAboveThreshold {
		t.Error("expected NoMatchesAboveThreshold=true when no pair is a confident match")
	}
	if resp.Message == "" {
		t.Error("expected a non-empty message prompting the user to refine their query")
	}
	// Suggestions should carry the raw venue markets (up to 3 each).
	if len(resp.Suggestions.Kalshi) == 0 {
		t.Error("expected at least 1 kalshi suggestion")
	}
	if len(resp.Suggestions.Polymarket) == 0 {
		t.Error("expected at least 1 polymarket suggestion")
	}
}

func TestSearchEndpointFetchesBothVenues(t *testing.T) {
	kalshiConn := &mockConnector{name: "kalshi", markets: nil}
	polyConn := &mockConnector{name: "polymarket", markets: nil}

	kMarket := testMarket("kalshi", "k-spy")
	pMarket := testMarket("polymarket", "p-spy")
	kalshiConn.markets = []models.Market{kMarket}
	polyConn.markets = []models.Market{pMarket}

	callLog := make(map[string]bool)
	spy := &spyConnector{inner: kalshiConn, log: callLog}
	spy2 := &spyConnector{inner: polyConn, log: callLog}

	srv := server.NewServer(
		testFS,
		[]server.VenueConnector{spy, spy2},
		&mockDetector{result: models.MatchResult{IsMatch: false}},
		&mockRouter{},
		silentLogger(),
	)

	req := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if !callLog["kalshi"] {
		t.Error("kalshi connector was not called")
	}
	if !callLog["polymarket"] {
		t.Error("polymarket connector was not called")
	}
	_ = rec
}

func TestSearchEndpointHandlesVenueError(t *testing.T) {
	// Kalshi fails, polymarket succeeds — endpoint should still return 200 with no-match response.
	kalshiConn := &mockConnector{name: "kalshi", err: &testError{"kalshi down"}}
	polyConn := &mockConnector{name: "polymarket", markets: []models.Market{testMarket("polymarket", "p1")}}

	srv := server.NewServer(
		testFS,
		[]server.VenueConnector{kalshiConn, polyConn},
		&mockDetector{result: models.MatchResult{IsMatch: true}},
		&mockRouter{},
		silentLogger(),
	)

	req := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even when one venue errors, got %d", rec.Code)
	}

	var resp models.SearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

func TestSearchEndpointReturnsObjectNotNull(t *testing.T) {
	srv := server.NewServer(testFS, nil, &mockDetector{}, &mockRouter{}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/search?q=empty", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	body := strings.TrimSpace(rec.Body.String())
	if !strings.HasPrefix(body, "{") {
		t.Errorf("expected JSON object, got: %s", body)
	}
}

// ── /route ────────────────────────────────────────────────────────────────────

func TestRouteEndpointReturnsDecision(t *testing.T) {
	kMarket := testMarket("kalshi", "k-route-1")
	pMarket := testMarket("polymarket", "p-route-1")

	kalshiConn := &mockConnector{name: "kalshi", markets: []models.Market{kMarket}}
	polyConn := &mockConnector{name: "polymarket", markets: []models.Market{pMarket}}

	match := testMatch(kMarket, pMarket)
	detector := &mockDetector{result: models.MatchResult{IsMatch: true, Confidence: 0.90}}
	router := &mockRouter{decision: testDecision("kalshi")}

	srv := server.NewServer(
		testFS,
		[]server.VenueConnector{kalshiConn, polyConn},
		detector,
		router,
		silentLogger(),
	)

	// First, populate the cache via a search.
	searchReq := httptest.NewRequest(http.MethodGet, "/search?q=test", nil)
	srv.ServeHTTP(httptest.NewRecorder(), searchReq)

	// Now route using MarketA's ID.
	body := map[string]any{"market_id": match.MarketA.ID, "side": "yes", "size": 500}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/route", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var decision models.RoutingDecision
	if err := json.NewDecoder(rec.Body).Decode(&decision); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if decision.RecommendedVenue != "kalshi" {
		t.Errorf("expected recommended venue kalshi, got %q", decision.RecommendedVenue)
	}
}

func TestRouteEndpointHandlesInvalidJSON(t *testing.T) {
	srv := server.NewServer(testFS, nil, &mockDetector{}, &mockRouter{}, silentLogger())

	req := httptest.NewRequest(http.MethodPost, "/route", strings.NewReader("{not valid json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestRouteEndpointRequiresMarketID(t *testing.T) {
	srv := server.NewServer(testFS, nil, &mockDetector{}, &mockRouter{}, silentLogger())

	body := map[string]any{"side": "yes", "size": 500}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/route", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestRouteEndpointMarketNotFound(t *testing.T) {
	srv := server.NewServer(testFS, nil, &mockDetector{}, &mockRouter{}, silentLogger())

	body := map[string]any{"market_id": "nonexistent-id", "side": "yes", "size": 500}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/route", bytes.NewReader(b))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestRouteEndpointMethodNotAllowed(t *testing.T) {
	srv := server.NewServer(testFS, nil, &mockDetector{}, &mockRouter{}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/route", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

// ── GET / ─────────────────────────────────────────────────────────────────────

func TestRootServesHTML(t *testing.T) {
	srv := server.NewServer(testFS, nil, &mockDetector{}, &mockRouter{}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("expected text/html content-type, got %q", ct)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Equinox") {
		t.Error("expected HTML body to contain 'Equinox'")
	}
}

func TestRootReturns404ForUnknownPaths(t *testing.T) {
	srv := server.NewServer(testFS, nil, &mockDetector{}, &mockRouter{}, silentLogger())

	req := httptest.NewRequest(http.MethodGet, "/unknown-path", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// ── Test helpers ──────────────────────────────────────────────────────────────

// spyConnector wraps a VenueConnector and records when it was called.
type spyConnector struct {
	inner server.VenueConnector
	log   map[string]bool
}

func (s *spyConnector) FetchMarkets(ctx context.Context, q string) ([]models.Market, error) {
	s.log[s.inner.GetVenueName()] = true
	return s.inner.FetchMarkets(ctx, q)
}
func (s *spyConnector) GetVenueName() string { return s.inner.GetVenueName() }

// testError is a minimal error type for tests.
type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
