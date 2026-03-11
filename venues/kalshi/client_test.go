package kalshi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/equinox/config"
	"github.com/equinox/logger"
	"github.com/equinox/venues"
	"github.com/equinox/venues/kalshi"
)

func newTestClient(t *testing.T, serverURL string) *kalshi.KalshiClient {
	t.Helper()

	cfg := &config.Config{
		KalshiBaseURL: serverURL,
		HTTPTimeout:   5 * time.Second,
	}
	log := logger.New(io.Discard)
	client, err := kalshi.NewKalshiClient(cfg, log)
	if err != nil {
		t.Fatalf("NewKalshiClient: %v", err)
	}
	kalshi.SetRetryBaseDelay(client, 0)
	kalshi.SetRateLimiter(client, rate.NewLimiter(rate.Inf, 1))
	return client
}

func oneSearchResponse() kalshi.SearchResponse {
	return kalshi.SearchResponse{
		CurrentPage: []kalshi.KalshiSeriesResult{
			{
				SeriesTicker: "KXTEST",
				SeriesTitle:  "Test Market Series",
				EventTicker:  "KXTEST-26NOV01",
				EventTitle:   "Test Market Event",
				Category:     "other",
				Markets: []kalshi.KalshiMarket{
					{
						Ticker:            "KXTEST-26NOV01-T1",
						YesSubtitle:       "Will the test market resolve Yes?",
						YesBidDollars:     "0.44",
						YesAskDollars:     "0.46",
						CloseTS:           "2026-11-01T00:00:00Z",
						Status:            "open",
						Volume:            1200,
						Score:             900,
						CustomStrike:      map[string]string{"Price": "$44"},
						RulebookVariables: map[string]string{"image_link": "https://example.com"},
					},
				},
			},
		},
	}
}

func TestKalshiFetchReturnsMarkets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(oneSearchResponse()); err != nil {
			t.Errorf("mock server encode error: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	markets, err := client.FetchMarkets(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(markets) != 1 {
		t.Fatalf("got %d markets, want 1", len(markets))
	}

	m := markets[0]
	if m.Venue != "kalshi" {
		t.Errorf("Venue = %q, want %q", m.Venue, "kalshi")
	}
	if m.VenueID != "KXTEST-26NOV01-T1" {
		t.Errorf("VenueID = %q, want %q", m.VenueID, "KXTEST-26NOV01-T1")
	}
	if m.YesBid != 0.44 {
		t.Errorf("YesBid = %v, want 0.44", m.YesBid)
	}
}

func TestKalshiFetchSetsAcceptHeader(t *testing.T) {
	var capturedHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oneSearchResponse())
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.FetchMarkets(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedHeader != "application/json" {
		t.Errorf("Accept header = %q, want %q", capturedHeader, "application/json")
	}
}

func TestKalshiFetchWithQuerySendsURLParams(t *testing.T) {
	var capturedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(kalshi.SearchResponse{})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.FetchMarkets(context.Background(), "bitcoin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedQuery == "" {
		t.Fatal("expected query parameters in request URL, got empty")
	}
	if want := "query=bitcoin"; !contains(capturedQuery, want) {
		t.Errorf("request query = %q, want substring %q", capturedQuery, want)
	}
	if want := "order_by=querymatch"; !contains(capturedQuery, want) {
		t.Errorf("request query = %q, want substring %q", capturedQuery, want)
	}
	if want := "status=open"; !contains(capturedQuery, want) {
		t.Errorf("request query = %q, want substring %q", capturedQuery, want)
	}
}

func TestKalshiFetchDoesNotSendLegacyAuthHeaders(t *testing.T) {
	var (
		accessKey       string
		accessTimestamp string
		accessSignature string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accessKey = r.Header.Get("KALSHI-ACCESS-KEY")
		accessTimestamp = r.Header.Get("KALSHI-ACCESS-TIMESTAMP")
		accessSignature = r.Header.Get("KALSHI-ACCESS-SIGNATURE")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(kalshi.SearchResponse{})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.FetchMarkets(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if accessKey != "" || accessTimestamp != "" || accessSignature != "" {
		t.Errorf("legacy auth headers should be absent, got key=%q timestamp=%q signature=%q",
			accessKey, accessTimestamp, accessSignature)
	}
}

func TestKalshiFetchHandlesTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err := client.FetchMarkets(ctx, "test")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestKalshiFetchHandlesNon200(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{"429 Too Many Requests", http.StatusTooManyRequests},
		{"500 Internal Server Error", http.StatusInternalServerError},
		{"401 Unauthorized", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
			}))
			defer server.Close()

			client := newTestClient(t, server.URL)
			_, err := client.FetchMarkets(context.Background(), "test")
			if err == nil {
				t.Fatalf("expected error for status %d, got nil", tt.status)
			}
		})
	}
}

func TestKalshiFetchHandlesMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{this is not: valid json[[[`)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.FetchMarkets(context.Background(), "test")
	if err == nil {
		t.Fatal("expected JSON decode error, got nil")
	}
}

func TestKalshiFetchHandlesEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(kalshi.SearchResponse{CurrentPage: []kalshi.KalshiSeriesResult{}})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	markets, err := client.FetchMarkets(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(markets) != 0 {
		t.Errorf("got %d markets, want 0", len(markets))
	}
}

func TestKalshiGetVenueName(t *testing.T) {
	client := newTestClient(t, "http://localhost")
	if got := client.GetVenueName(); got != "kalshi" {
		t.Errorf("GetVenueName() = %q, want %q", got, "kalshi")
	}
}

func TestKalshiFetchLimitsToTopTenMarkets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := kalshi.SearchResponse{
			CurrentPage: []kalshi.KalshiSeriesResult{
				{
					SeriesTicker: "KXTEST",
					SeriesTitle:  "Test Market Series",
					EventTicker:  "KXTEST-26NOV01",
					EventTitle:   "Test Market Event",
					Category:     "other",
					Markets:      make([]kalshi.KalshiMarket, 0, 12),
				},
			},
		}
		for i := 1; i <= 12; i++ {
			resp.CurrentPage[0].Markets = append(resp.CurrentPage[0].Markets, kalshi.KalshiMarket{
				Ticker:        fmt.Sprintf("KXTEST-26NOV01-T%d", i),
				YesSubtitle:   fmt.Sprintf("Will test market %d resolve Yes?", i),
				YesBidDollars: "0.44",
				YesAskDollars: "0.46",
				CloseTS:       "2026-11-01T00:00:00Z",
				Status:        "open",
			})
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("mock server encode error: %v", err)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	markets, err := client.FetchMarkets(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(markets) != venues.MaxMarketsPerVenue {
		t.Fatalf("got %d markets, want %d", len(markets), venues.MaxMarketsPerVenue)
	}
	if markets[len(markets)-1].VenueID != "KXTEST-26NOV01-T10" {
		t.Fatalf("last returned market = %q, want %q", markets[len(markets)-1].VenueID, "KXTEST-26NOV01-T10")
	}
}

func TestKalshiFetchSkipsInvalidMarkets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := kalshi.SearchResponse{
			CurrentPage: []kalshi.KalshiSeriesResult{
				{
					EventTicker: "KXGOOD-26NOV01",
					EventTitle:  "Good Test Event",
					Category:    "other",
					Markets: []kalshi.KalshiMarket{
						{
							Ticker:        "KXGOOD-26NOV01-T1",
							YesBidDollars: "0.50",
							YesAskDollars: "0.52",
							CloseTS:       "2026-11-01T00:00:00Z",
							Status:        "open",
							Volume:        50,
						},
						{
							Ticker:        "",
							YesBidDollars: "0.50",
							YesAskDollars: "0.52",
							CloseTS:       "2026-11-01T00:00:00Z",
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	markets, err := client.FetchMarkets(context.Background(), "good")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(markets) != 1 {
		t.Errorf("got %d markets, want 1 (invalid market should be skipped)", len(markets))
	}
}

func TestKalshiSearchSeriesBuildsCursorRequest(t *testing.T) {
	var capturedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(kalshi.SearchResponse{})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.SearchSeries(context.Background(), kalshi.SearchParams{
		Query:    "bitcoin",
		PageSize: 10,
		Cursor:   "CAI",
		OrderBy:  "querymatch",
		Status:   "open",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(capturedQuery, "cursor=CAI") {
		t.Errorf("request query = %q, want cursor parameter", capturedQuery)
	}
}

func TestKalshiGetSeriesDecodesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(kalshi.SeriesResponse{
			Series: []kalshi.SeriesDetail{{Ticker: "KXBTC", Title: "Bitcoin"}},
		})
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	series, err := client.GetSeries(context.Background(), "KXBTC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(series) != 1 || series[0].Ticker != "KXBTC" {
		t.Errorf("GetSeries() = %#v, want one KXBTC entry", series)
	}
}

func TestKalshiRetryOn429ThenSuccess(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(oneSearchResponse())
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	markets, err := client.FetchMarkets(context.Background(), "test")
	if err != nil {
		t.Fatalf("expected success after retries, got error: %v", err)
	}
	if len(markets) != 1 {
		t.Errorf("want 1 market, got %d", len(markets))
	}
	if got := int(attempts.Load()); got != 3 {
		t.Errorf("want 3 server hits (2x429 + 1x200), got %d", got)
	}
}

func TestKalshiRetryOn429ExhaustsRetries(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	_, err := client.FetchMarkets(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}
	if got := int(attempts.Load()); got != 4 {
		t.Errorf("want 4 server hits (1 initial + 3 retries), got %d", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
