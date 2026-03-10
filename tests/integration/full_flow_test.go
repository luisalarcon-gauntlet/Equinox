// Package integration contains end-to-end tests for Project Equinox.
//
// These tests wire up real venue clients, a real equivalence detector (with a
// mock AI layer to avoid live Anthropic calls), a real routing engine, and a
// real HTTP server — all connected to mock httptest.Servers that stand in for
// the Kalshi and Polymarket APIs.
//
// Every test runs in isolation with fresh components and does not depend on
// external network access or environment variables.
package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	aipackage "github.com/equinox/ai"
	"github.com/equinox/config"
	"github.com/equinox/equivalence"
	"github.com/equinox/equivalence/tools"
	"github.com/equinox/logger"
	"github.com/equinox/models"
	"github.com/equinox/routing"
	"github.com/equinox/server"
	"github.com/equinox/venues"
	"github.com/equinox/venues/kalshi"
	"github.com/equinox/venues/polymarket"
)

// ── Mock AI client ────────────────────────────────────────────────────────────

// mockAIClient implements equivalence.AIEvaluator for tests.
// It returns a fixed EquivalenceResult or an error, enabling tests to
// exercise both the happy path and the AI-unavailable degradation path.
type mockAIClient struct {
	result aipackage.EquivalenceResult
	err    error
}

func (m *mockAIClient) EvaluateEquivalence(
	_ context.Context,
	_, _ models.Market,
	_ []tools.ToolResult,
) (aipackage.EquivalenceResult, error) {
	return m.result, m.err
}

// ── Test helpers ──────────────────────────────────────────────────────────────

// silentLogger returns a logger that discards all output, keeping test output clean.
func silentLogger() *logger.Logger {
	return logger.New(io.Discard)
}

// testFS is a minimal in-memory filesystem with a placeholder index.html.
var testFS = fstest.MapFS{
	"index.html": {Data: []byte("<html><body>Equinox Integration Test</body></html>")},
}

// kalshiResponse builds a Kalshi GET /events JSON response with one event
// containing one market leg. This matches the KalshiEventsResponse format
// expected by SearchAllOpenEvents.
func kalshiResponse(ticker, title, status, yesBid, yesAsk, closeTime string) string {
	return fmt.Sprintf(`{
		"events": [{
			"event_ticker": %q,
			"series_ticker": "HOUSE",
			"title": %q,
			"category": "Politics",
			"markets": [{
				"ticker": %q,
				"event_ticker": %q,
				"yes_sub_title": %q,
				"title": %q,
				"status": %q,
				"yes_bid_dollars": %q,
				"yes_ask_dollars": %q,
				"no_bid_dollars": "0.0200",
				"no_ask_dollars": "0.0400",
				"open_interest": 80000,
				"open_interest_fp": "80000",
				"volume_24h": 50000,
				"close_time": %q
			}]
		}],
		"cursor": ""
	}`, "HOUSE-2026", title, ticker, "HOUSE-2026", title, title, status, yesBid, yesAsk, closeTime)
}

// polymarketResponse builds a Polymarket /public-search JSON response with one
// event containing one market. This matches the SearchResponse format expected
// by SearchActiveMarkets.
func polymarketResponse(id, question string, yesBid, yesAsk, liquidity float64, endDate string, active bool) string {
	return fmt.Sprintf(`{
		"events": [{
			"title": %q,
			"slug": %q,
			"active": %t,
			"closed": false,
			"archived": false,
			"markets": [{
				"id": %q,
				"question": %q,
				"outcomePrices": "[\"%.4f\",\"%.4f\"]",
				"active": %t,
				"closed": false,
				"archived": false,
				"endDate": %q,
				"volume24hr": 60000,
				"liquidityNum": %f
			}]
		}]
	}`, question, id, active, id, question, yesBid, 1.0-yesBid, active, endDate, liquidity)
}

// generateTestKeyFile creates a temporary RSA private key PEM file for use in
// integration tests. The file is removed automatically via t.Cleanup.
func generateTestKeyFile(t *testing.T) (keyID, keyPath string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generateTestKeyFile: rsa.GenerateKey: %v", err)
	}

	keyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("generateTestKeyFile: MarshalPKCS8PrivateKey: %v", err)
	}

	tmpFile, err := os.CreateTemp("", "equinox-integration-kalshi-key-*.pem")
	if err != nil {
		t.Fatalf("generateTestKeyFile: CreateTemp: %v", err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	if err := pem.Encode(tmpFile, &pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}); err != nil {
		t.Fatalf("generateTestKeyFile: pem.Encode: %v", err)
	}
	tmpFile.Close()

	return "integration-test-key-id", tmpFile.Name()
}

// buildServer wires up the full Equinox stack against the provided mock venue
// HTTP servers and AI client. Returns the HTTP handler ready for httptest.
func buildServer(
	t *testing.T,
	kalshiServer, polyServer *httptest.Server,
	aiClient equivalence.AIEvaluator,
	stalenessThreshold time.Duration,
) *server.Server {
	t.Helper()

	keyID, keyPath := generateTestKeyFile(t)

	cfg := &config.Config{
		AnthropicAPIKey:              "test-key",
		KalshiAPIKeyID:               keyID,
		KalshiAPIKeyPath:             keyPath,
		KalshiBaseURL:                kalshiServer.URL,
		PolymarketBaseURL:            polyServer.URL,
		HTTPTimeout:                  5 * time.Second,
		HeuristicConfidenceThreshold: 0.80,
		PriceDataStalenessThreshold:  stalenessThreshold,
	}
	log := silentLogger()

	kalshiClient, err := kalshi.NewKalshiClient(cfg, log)
	if err != nil {
		t.Fatalf("NewKalshiClient: %v", err)
	}
	polyClient := polymarket.NewPolymarketClient(cfg, log)
	connectors := []venues.VenueConnector{kalshiClient, polyClient}

	detector := equivalence.NewDetector(cfg.HeuristicConfidenceThreshold, aiClient, log)
	engine := routing.NewEngine(cfg.PriceDataStalenessThreshold, log)

	return server.NewServer(testFS, connectors, detector, engine, log)
}

// doSearch performs a GET /search?q={query} against the server and returns the
// decoded MatchResult slice.
func doSearch(t *testing.T, srv *server.Server, query string) []models.MatchResult {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/search?q="+query, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: expected 200, got %d — body: %s", rec.Code, rec.Body.String())
	}
	var results []models.MatchResult
	if err := json.NewDecoder(rec.Body).Decode(&results); err != nil {
		t.Fatalf("search: failed to decode response: %v", err)
	}
	return results
}

// doRoute performs a POST /route against the server and returns the decoded
// RoutingDecision.
func doRoute(t *testing.T, srv *server.Server, marketID, side string, size float64) (models.RoutingDecision, int) {
	t.Helper()
	body := map[string]any{"market_id": marketID, "side": side, "size": size}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/route", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return models.RoutingDecision{}, rec.Code
	}
	var decision models.RoutingDecision
	if err := json.NewDecoder(rec.Body).Decode(&decision); err != nil {
		t.Fatalf("route: failed to decode response: %v", err)
	}
	return decision, rec.Code
}

// ── Integration Tests ─────────────────────────────────────────────────────────

// TestFullFlowSearchAndRoute runs the complete happy-path flow:
// mock APIs → search → equivalence match → route → routing decision.
//
// Both venue titles are sufficiently similar for the heuristic to confirm a
// match (entity overlap on "democrats", "control", "house", "2026" with the
// same resolution date produces confidence ≥ 0.80).
func TestFullFlowSearchAndRoute(t *testing.T) {
	const closeTime = "2026-11-04T00:00:00Z"

	// Kalshi mock: one matching market.
	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, kalshiResponse(
			"HOUSE-DEM-2026",
			"Will Democrats control the House in 2026",
			"active",
			"0.4500", "0.4700",
			closeTime,
		))
	}))
	defer kalshiSrv.Close()

	// Polymarket mock: one matching market (same event, same date, very similar title).
	polySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, polymarketResponse(
			"poly-house-dem-2026",
			"Will Democrats control House in 2026",
			0.44, 0.48, 240000.0,
			closeTime,
			true,
		))
	}))
	defer polySrv.Close()

	// AI client is not expected to be called (heuristic confidence should be ≥ 0.80).
	ai := &mockAIClient{result: aipackage.EquivalenceResult{IsEquivalent: true, Confidence: 0.95}}
	srv := buildServer(t, kalshiSrv, polySrv, ai, 2*time.Minute)

	// Step 1: Search. Use a single keyword that's an exact substring of both
	// venue titles (Kalshi's client-side filter uses strings.Contains).
	results := doSearch(t, srv, "democrats")

	if len(results) == 0 {
		t.Fatal("expected at least one matched market pair, got none")
	}

	match := results[0]
	if !match.IsMatch {
		t.Errorf("expected IsMatch=true, got false")
	}
	if match.Confidence <= 0 {
		t.Errorf("expected positive confidence, got %.2f", match.Confidence)
	}
	if match.MarketA.ID == "" || match.MarketB.ID == "" {
		t.Error("expected both MarketA and MarketB to have IDs")
	}

	// Step 2: Route using MarketA's ID.
	decision, code := doRoute(t, srv, match.MarketA.ID, "yes", 500)
	if code != http.StatusOK {
		t.Fatalf("route: expected 200, got %d", code)
	}
	if decision.RecommendedVenue == "" {
		t.Error("expected RecommendedVenue to be set")
	}
	if decision.Reasoning == "" {
		t.Error("expected Reasoning to be non-empty")
	}
	if decision.Confidence < 0 || decision.Confidence > 1 {
		t.Errorf("expected Confidence in [0,1], got %.2f", decision.Confidence)
	}
	if len(decision.VenueScores) != 2 {
		t.Errorf("expected 2 VenueScores, got %d", len(decision.VenueScores))
	}
}

// TestFullFlowBothVenuesUnavailable tests graceful degradation when both venue
// API servers return HTTP 500. The search endpoint should still return 200 with
// an empty result array rather than propagating the error to the caller.
func TestFullFlowBothVenuesUnavailable(t *testing.T) {
	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer kalshiSrv.Close()

	polySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer polySrv.Close()

	ai := &mockAIClient{}
	srv := buildServer(t, kalshiSrv, polySrv, ai, 2*time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/search?q=elections", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 even when both venues fail, got %d", rec.Code)
	}

	// Response must be a JSON array (possibly empty) — not an error object.
	body := strings.TrimSpace(rec.Body.String())
	if !strings.HasPrefix(body, "[") {
		t.Errorf("expected JSON array response, got: %s", body)
	}

	var results []models.MatchResult
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&results); err != nil {
		t.Fatalf("response is not valid JSON array: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results when both venues fail, got %d", len(results))
	}
}

// TestFullFlowAILayerUnavailable verifies that when heuristic confidence is
// below the 0.80 threshold and the AI layer returns an error, the detector
// falls back gracefully to a heuristic-only result with a warning.
//
// The market titles are intentionally dissimilar (different terminology for the
// same concept) so the heuristic produces low entity overlap (< 0.80). This
// forces escalation to the AI layer, which then errors, triggering the fallback.
func TestFullFlowAILayerUnavailable(t *testing.T) {
	const closeTime = "2026-12-31T00:00:00Z"

	// Use different terminology to produce low heuristic entity overlap:
	// "btc" and "bitcoin" are different tokens from the heuristic's perspective.
	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, kalshiResponse(
			"BTC-100K-2026",
			"Will BTC price exceed 100000 in 2026",
			"active",
			"0.3500", "0.3700",
			closeTime,
		))
	}))
	defer kalshiSrv.Close()

	polySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, polymarketResponse(
			"poly-bitcoin-100k-2026",
			"Bitcoin above 100k end of year 2026",
			0.34, 0.38, 150000.0,
			closeTime,
			true,
		))
	}))
	defer polySrv.Close()

	// AI layer is unavailable.
	ai := &mockAIClient{err: fmt.Errorf("anthropic: connection refused")}
	srv := buildServer(t, kalshiSrv, polySrv, ai, 2*time.Minute)

	results := doSearch(t, srv, "btc")

	// With AI down, the detector falls back to heuristic-only. The pair may or
	// may not be returned as a match depending on whether heuristic confidence
	// exceeds the fallback threshold (0.50). Either way the server must not
	// crash and must return a valid JSON array.
	if results == nil {
		t.Fatal("expected a non-nil result slice even when AI is unavailable")
	}

	// Any matches returned under AI-unavailable conditions must have the
	// "heuristic-only" method and carry the AI-unavailable warning.
	for _, m := range results {
		if m.Method != "heuristic-only" {
			t.Errorf("expected Method='heuristic-only', got %q", m.Method)
		}
		hasWarning := false
		for _, w := range m.Warnings {
			if strings.Contains(w, "AI layer unavailable") {
				hasWarning = true
				break
			}
		}
		if !hasWarning {
			t.Errorf("expected AI-unavailable warning in Warnings, got: %v", m.Warnings)
		}
	}
}

// TestFullFlowOppositeMarketDetection verifies that markets which represent
// opposite sides of the same event (GOP win = Dem loss) are correctly
// identified as equivalent with AreOpposites=true.
//
// Titles are designed to have moderate entity overlap so the heuristic cannot
// confirm a match (entity score below 0.80). The mock AI client returns
// is_equivalent=true, are_opposites=true, simulating what Claude would return
// after seeing check_opposites detect the GOP/Democrat antonym.
func TestFullFlowOppositeMarketDetection(t *testing.T) {
	const closeTime = "2026-11-04T00:00:00Z"

	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, kalshiResponse(
			"HOUSE-GOP-2026",
			"Will Republicans win the House in 2026",
			"active",
			"0.5400", "0.5600",
			closeTime,
		))
	}))
	defer kalshiSrv.Close()

	polySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, polymarketResponse(
			"poly-house-dem-2026",
			"Will Democrats control the House in 2026",
			0.43, 0.47, 200000.0,
			closeTime,
			true,
		))
	}))
	defer polySrv.Close()

	// AI correctly identifies these as opposite sides of the same event.
	ai := &mockAIClient{
		result: aipackage.EquivalenceResult{
			IsEquivalent: true,
			AreOpposites: true,
			Confidence:   0.88,
			Reasoning:    "GOP win is the complement of Democrat control — mirror images of the same House outcome",
			UsedAILayer:  true,
		},
	}
	srv := buildServer(t, kalshiSrv, polySrv, ai, 2*time.Minute)

	results := doSearch(t, srv, "house")

	if len(results) == 0 {
		t.Fatal("expected at least one match for opposite market pair")
	}

	match := results[0]
	if !match.IsMatch {
		t.Errorf("expected IsMatch=true for opposite markets, got false")
	}
	if !match.AreOpposites {
		t.Errorf("expected AreOpposites=true for GOP/Democrat opposite pair, got false")
	}
	if match.Confidence <= 0 {
		t.Errorf("expected positive confidence, got %.2f", match.Confidence)
	}
	if match.Method != "heuristic+ai" {
		t.Errorf("expected Method='heuristic+ai' (AI was needed), got %q", match.Method)
	}
}

// TestFullFlowNoMarketsFound verifies that a search returning no markets from
// either venue produces a 200 OK with an empty JSON array — not an error.
func TestFullFlowNoMarketsFound(t *testing.T) {
	// Both venues return valid JSON but zero markets.
	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"events": [], "cursor": ""}`)
	}))
	defer kalshiSrv.Close()

	polySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"events": []}`)
	}))
	defer polySrv.Close()

	ai := &mockAIClient{}
	srv := buildServer(t, kalshiSrv, polySrv, ai, 2*time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/search?q=nonexistent+topic", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 when no markets found, got %d", rec.Code)
	}

	body := strings.TrimSpace(rec.Body.String())
	if !strings.HasPrefix(body, "[") {
		t.Errorf("expected JSON array response, got: %s", body)
	}

	var results []models.MatchResult
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&results); err != nil {
		t.Fatalf("response is not valid JSON array: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results when no markets exist, got %d", len(results))
	}
}

// TestFullFlowHealthEndpoint checks that /health returns {"status":"ok"}
// in a full integration context.
func TestFullFlowHealthEndpoint(t *testing.T) {
	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"events": [], "cursor": ""}`)
	}))
	defer kalshiSrv.Close()
	polySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"events": []}`)
	}))
	defer polySrv.Close()

	srv := buildServer(t, kalshiSrv, polySrv, &mockAIClient{}, 2*time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("health response is not valid JSON: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
}

// TestFullFlowStaleDataProducesWarning verifies that routing a market with
// data older than the staleness threshold produces a warning in the decision.
//
// The test uses a 10ms staleness threshold, searches to populate the cache
// (setting FetchedAt = time.Now()), then sleeps 20ms before routing so that
// the cached market data is provably older than the threshold.
func TestFullFlowStaleDataProducesWarning(t *testing.T) {
	const closeTime = "2026-11-04T00:00:00Z"

	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, kalshiResponse(
			"HOUSE-DEM-STALE",
			"Will Democrats control the House in 2026",
			"active",
			"0.4500", "0.4700",
			closeTime,
		))
	}))
	defer kalshiSrv.Close()

	polySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, polymarketResponse(
			"poly-house-dem-stale",
			"Will Democrats control House in 2026",
			0.44, 0.48, 240000.0,
			closeTime,
			true,
		))
	}))
	defer polySrv.Close()

	ai := &mockAIClient{result: aipackage.EquivalenceResult{IsEquivalent: true, Confidence: 0.92}}
	// 10ms staleness threshold: any data older than 10ms is stale.
	const staleThreshold = 10 * time.Millisecond
	srv := buildServer(t, kalshiSrv, polySrv, ai, staleThreshold)

	results := doSearch(t, srv, "democrats")
	if len(results) == 0 {
		t.Fatal("expected at least one match")
	}

	// Sleep long enough to make the cached FetchedAt older than the threshold.
	time.Sleep(2 * staleThreshold)

	match := results[0]
	decision, code := doRoute(t, srv, match.MarketA.ID, "yes", 500)
	if code != http.StatusOK {
		t.Fatalf("route: expected 200, got %d", code)
	}

	hasStaleWarning := false
	for _, w := range decision.Warnings {
		if strings.Contains(w, "stale data") {
			hasStaleWarning = true
			break
		}
	}
	if !hasStaleWarning {
		t.Errorf("expected stale data warning in routing decision, got warnings: %v", decision.Warnings)
	}
}

// TestFullFlowRouteWithoutSearch verifies that calling /route with an unknown
// market ID (no prior /search) returns 404.
func TestFullFlowRouteWithoutSearch(t *testing.T) {
	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"events": [], "cursor": ""}`)
	}))
	defer kalshiSrv.Close()
	polySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"events": []}`)
	}))
	defer polySrv.Close()

	srv := buildServer(t, kalshiSrv, polySrv, &mockAIClient{}, 2*time.Minute)

	_, code := doRoute(t, srv, "nonexistent-market-id", "yes", 500)
	if code != http.StatusNotFound {
		t.Errorf("expected 404 when routing without prior search, got %d", code)
	}
}
