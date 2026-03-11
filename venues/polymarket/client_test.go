package polymarket

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/equinox/logger"
	"github.com/equinox/venues"
)

func newPolyTestClient(t *testing.T, handler http.Handler) *PolymarketClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &PolymarketClient{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
		log:        logger.New(io.Discard),
	}
}

// serveSearchResponse serves a SearchResponse JSON envelope for /public-search.
func serveSearchResponse(t *testing.T, events []Event) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SearchResponse{Events: events})
	})
}

// ── FetchMarkets integration tests ───────────────────────────────────────────

func TestFetchMarkets_RetainsOnlyFreshActiveMarkets(t *testing.T) {
	future := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)

	events := []Event{
		{
			Title:  "Test Event",
			Active: true,
			Markets: []Market{
				{
					ID:            "fresh-active",
					Question:      "Will X happen in 2027?",
					Active:        true,
					EndDate:       future,
					OutcomePrices: `["0.50","0.50"]`,
				},
				{
					ID:            "stale-active",
					Question:      "Did Y happen?",
					Active:        true,
					EndDate:       past, // past → should be filtered out
					OutcomePrices: `["0.90","0.10"]`,
				},
				{
					ID:            "fresh-inactive",
					Question:      "Will Z happen?",
					Active:        false, // inactive → should be filtered out
					EndDate:       future,
					OutcomePrices: `["0.50","0.50"]`,
				},
			},
		},
	}

	client := newPolyTestClient(t, serveSearchResponse(t, events))
	markets, err := client.FetchMarkets(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(markets) != 1 {
		t.Fatalf("want 1 market (fresh+active only), got %d", len(markets))
	}
	if markets[0].VenueID != "fresh-active" {
		t.Errorf("want fresh-active, got %s", markets[0].VenueID)
	}
}

func TestFetchMarkets_NoDatePassesThrough(t *testing.T) {
	events := []Event{
		{
			Title:  "No Date Event",
			Active: true,
			Markets: []Market{
				{
					ID:            "no-date",
					Question:      "Will something happen?",
					Active:        true,
					OutcomePrices: `["0.50","0.50"]`,
					// no EndDate — should pass through
				},
			},
		},
	}

	client := newPolyTestClient(t, serveSearchResponse(t, events))
	markets, err := client.FetchMarkets(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(markets) != 1 {
		t.Errorf("market with no date should pass through, got %d markets", len(markets))
	}
}

func TestFetchMarkets_NonOKStatusReturnsError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	client := newPolyTestClient(t, handler)

	_, err := client.FetchMarkets(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error on non-200 response, got nil")
	}
}

func TestFetchMarkets_MalformedJSONReturnsError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	})
	client := newPolyTestClient(t, handler)

	_, err := client.FetchMarkets(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
}

func TestFetchMarkets_InactiveEventSkipped(t *testing.T) {
	events := []Event{
		{
			Title:  "Inactive Event",
			Active: false,
			Markets: []Market{
				{
					ID:            "in-inactive-event",
					Question:      "Will this resolve?",
					Active:        true,
					OutcomePrices: `["0.50","0.50"]`,
				},
			},
		},
	}

	client := newPolyTestClient(t, serveSearchResponse(t, events))
	markets, err := client.FetchMarkets(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(markets) != 0 {
		t.Errorf("inactive events should be skipped, got %d markets", len(markets))
	}
}

func TestFetchMarkets_LimitsToTopTenMarkets(t *testing.T) {
	future := time.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	markets := make([]Market, 0, 12)
	for i := 1; i <= 12; i++ {
		markets = append(markets, Market{
			ID:            "market-" + strconv.Itoa(i),
			Question:      "Will test outcome " + strconv.Itoa(i) + " happen in 2027?",
			Active:        true,
			EndDate:       future,
			OutcomePrices: `["0.50","0.50"]`,
		})
	}

	events := []Event{
		{
			Title:   "Test Event",
			Active:  true,
			Markets: markets,
		},
	}

	client := newPolyTestClient(t, serveSearchResponse(t, events))
	got, err := client.FetchMarkets(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != venues.MaxMarketsPerVenue {
		t.Fatalf("got %d markets, want %d", len(got), venues.MaxMarketsPerVenue)
	}
	if got[len(got)-1].VenueID != "market-10" {
		t.Fatalf("last returned market = %q, want %q", got[len(got)-1].VenueID, "market-10")
	}
}
