package kalshidb_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/equinox/logger"
	"github.com/equinox/venues/kalshidb"
)

func newTestClient(t *testing.T, serverURL, apiKey string) *kalshidb.Client {
	t.Helper()
	log := logger.New(io.Discard)
	return kalshidb.NewClient(serverURL, apiKey, 5*time.Second, log)
}

func TestSearchSendsQueryAndLimit(t *testing.T) {
	var path, apiKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		apiKey = r.Header.Get("X-API-Key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(kalshidb.SearchResponse{
			Query:   r.URL.Query().Get("q"),
			Results: nil,
			Meta:    kalshidb.SearchMeta{TotalResults: 0},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "test-key")
	ctx := context.Background()

	_, err := client.Search(ctx, "Bitcoin", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if path != "/v1/search" {
		t.Errorf("path = %q, want /v1/search", path)
	}
	if apiKey != "test-key" {
		t.Errorf("X-API-Key = %q, want test-key", apiKey)
	}
}

func TestSearchDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(kalshidb.SearchResponse{
			Query: "Bitcoin",
			Results: []kalshidb.SearchResult{
				{
					EventTicker:  "KXBTC-26MAR1322",
					SeriesTicker: "KXBTC",
					Title:        "Bitcoin price range on Mar 13, 2026 at 10pm EDT?",
					KalshiURL:    "https://api.elections.kalshi.com/trade-api/v2/events/KXBTC-26MAR1322?with_nested_markets=true",
					MatchSource:  "both",
				},
			},
			Meta: kalshidb.SearchMeta{TotalResults: 1, QueryTimeMs: 38},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "key")
	resp, err := client.Search(context.Background(), "Bitcoin", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Query != "Bitcoin" {
		t.Errorf("Query = %q, want Bitcoin", resp.Query)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(resp.Results))
	}
	if resp.Results[0].EventTicker != "KXBTC-26MAR1322" {
		t.Errorf("EventTicker = %q, want KXBTC-26MAR1322", resp.Results[0].EventTicker)
	}
	if resp.Results[0].KalshiURL == "" {
		t.Error("KalshiURL is empty")
	}
	if resp.Meta.TotalResults != 1 {
		t.Errorf("Meta.TotalResults = %d, want 1", resp.Meta.TotalResults)
	}
}

func TestSearchEmptyQueryReturnsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("should not call server for empty query")
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "key")
	resp, err := client.Search(context.Background(), "   ", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Query != "   " {
		t.Errorf("Query = %q", resp.Query)
	}
	if len(resp.Results) != 0 {
		t.Errorf("len(Results) = %d, want 0", len(resp.Results))
	}
}

func TestSearchNon200ReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, "key")
	_, err := client.Search(context.Background(), "test", 10)
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}
