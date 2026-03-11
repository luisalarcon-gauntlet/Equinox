// Package integration contains end-to-end tests for Project Equinox.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

func (m *mockAIClient) EvaluateBatch(
	_ context.Context,
	pairs []aipackage.BatchPair,
) ([]aipackage.EquivalenceResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	results := make([]aipackage.EquivalenceResult, len(pairs))
	for i := range pairs {
		results[i] = m.result
	}
	return results, nil
}

func silentLogger() *logger.Logger {
	return logger.New(io.Discard)
}

var testFS = fstest.MapFS{
	"index.html": {Data: []byte("<html><body>Equinox Integration Test</body></html>")},
}

// kalshiResponse builds a Kalshi GET /v1/search/series JSON response with one
// series result containing one nested market.
func kalshiResponse(ticker, title, status, yesBid, yesAsk, closeTime string) string {
	return fmt.Sprintf(`{
		"total_results_count": 1,
		"next_cursor": "",
		"current_page": [{
			"series_ticker": "HOUSE",
			"series_title": "US House Control",
			"event_ticker": "HOUSE-2026",
			"event_title": %q,
			"event_subtitle": "Election night",
			"category": "Politics",
			"total_series_volume": 1000000,
			"total_volume": 800000,
			"total_market_count": 1,
			"active_market_count": 1,
			"search_score": 100,
			"tags": ["politics","house"],
			"topic_keywords": ["house","election","democrats"],
			"markets": [{
				"ticker": %q,
				"yes_subtitle": %q,
				"status": %q,
				"yes_bid_dollars": %q,
				"yes_ask_dollars": %q,
				"volume": 50000,
				"close_ts": %q
			}]
		}]
	}`, title, ticker, title, status, yesBid, yesAsk, closeTime)
}

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

func buildServer(
	t *testing.T,
	kalshiServer, polyServer *httptest.Server,
	aiClient equivalence.AIEvaluator,
	stalenessThreshold time.Duration,
) *server.Server {
	t.Helper()

	cfg := &config.Config{
		OpenAIAPIKey:                 "test-key",
		OpenAIBaseURL:                "https://api.openai.com/v1",
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

func doSearch(t *testing.T, srv *server.Server, query string) models.SearchResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/search?q="+query, nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: expected 200, got %d - body: %s", rec.Code, rec.Body.String())
	}
	var resp models.SearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("search: failed to decode response: %v", err)
	}
	return resp
}

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

func TestFullFlowSearchAndRoute(t *testing.T) {
	const closeTime = "2026-11-04T00:00:00Z"

	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, kalshiResponse(
			"HOUSE-DEM-2026",
			"Will Democrats control the House in 2026",
			"open",
			"0.4500", "0.4700",
			closeTime,
		))
	}))
	defer kalshiSrv.Close()

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

	ai := &mockAIClient{result: aipackage.EquivalenceResult{IsMatch: true, Confidence: 0.95}}
	srv := buildServer(t, kalshiSrv, polySrv, ai, 2*time.Minute)

	resp := doSearch(t, srv, "democrats")

	if len(resp.Matches) == 0 {
		t.Fatal("expected at least one matched market pair, got none")
	}

	match := resp.Matches[0]
	if !match.IsMatch {
		t.Errorf("expected IsMatch=true, got false")
	}
	if match.Confidence <= 0 {
		t.Errorf("expected positive confidence, got %.2f", match.Confidence)
	}
	if match.MarketA.ID == "" || match.MarketB.ID == "" {
		t.Error("expected both MarketA and MarketB to have IDs")
	}

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

	var resp models.SearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON object: %v", err)
	}
	if len(resp.Matches) != 0 {
		t.Errorf("expected 0 matches when both venues fail, got %d", len(resp.Matches))
	}
	if !resp.NoMatchesAboveThreshold {
		t.Error("expected NoMatchesAboveThreshold=true when venues fail")
	}
}

func TestFullFlowAILayerUnavailable(t *testing.T) {
	const closeTime = "2026-12-31T00:00:00Z"

	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, kalshiResponse(
			"BTC-100K-2026",
			"Will BTC price exceed 100000 in 2026",
			"open",
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

	ai := &mockAIClient{err: fmt.Errorf("anthropic: connection refused")}
	srv := buildServer(t, kalshiSrv, polySrv, ai, 2*time.Minute)

	resp := doSearch(t, srv, "btc")

	for _, m := range resp.Matches {
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

func TestFullFlowOppositeMarketDetection(t *testing.T) {
	const closeTime = "2026-11-04T00:00:00Z"

	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, kalshiResponse(
			"HOUSE-GOP-2026",
			"Will Republicans win the House in 2026",
			"open",
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

	ai := &mockAIClient{
		result: aipackage.EquivalenceResult{
			IsMatch:     true,
			Confidence:  0.88,
			Reasoning:   "same House control market with inverse wording",
			UsedAILayer: true,
		},
	}
	srv := buildServer(t, kalshiSrv, polySrv, ai, 2*time.Minute)

	resp := doSearch(t, srv, "house")

	if len(resp.Matches) == 0 {
		t.Fatal("expected at least one match for opposite market pair")
	}

	match := resp.Matches[0]
	if !match.IsMatch {
		t.Errorf("expected IsMatch=true for opposite markets, got false")
	}
	if match.Confidence <= 0 {
		t.Errorf("expected positive confidence, got %.2f", match.Confidence)
	}
	if match.Method != "heuristic+ai" {
		t.Errorf("expected Method='heuristic+ai' (AI was needed), got %q", match.Method)
	}
}

func TestFullFlowNoMarketsFound(t *testing.T) {
	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"total_results_count":0,"next_cursor":"","current_page":[]}`)
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

	var resp models.SearchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("response is not valid JSON object: %v", err)
	}
	if len(resp.Matches) != 0 {
		t.Errorf("expected 0 matches when no markets exist, got %d", len(resp.Matches))
	}
	if !resp.NoMatchesAboveThreshold {
		t.Error("expected NoMatchesAboveThreshold=true when no markets exist")
	}
}

func TestFullFlowHealthEndpoint(t *testing.T) {
	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"total_results_count":0,"next_cursor":"","current_page":[]}`)
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

func TestFullFlowStaleDataProducesWarning(t *testing.T) {
	const closeTime = "2026-11-04T00:00:00Z"

	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, kalshiResponse(
			"HOUSE-DEM-STALE",
			"Will Democrats control the House in 2026",
			"open",
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

	ai := &mockAIClient{result: aipackage.EquivalenceResult{IsMatch: true, Confidence: 0.92}}
	const staleThreshold = 10 * time.Millisecond
	srv := buildServer(t, kalshiSrv, polySrv, ai, staleThreshold)

	searchResp := doSearch(t, srv, "democrats")
	if len(searchResp.Matches) == 0 {
		t.Fatal("expected at least one match")
	}

	time.Sleep(2 * staleThreshold)

	match := searchResp.Matches[0]
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

func TestFullFlowRouteWithoutSearch(t *testing.T) {
	kalshiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"total_results_count":0,"next_cursor":"","current_page":[]}`)
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
