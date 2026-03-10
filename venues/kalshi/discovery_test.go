package kalshi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"

	"github.com/equinox/logger"
)

// newDiscoveryTestClient wires a KalshiClient to handler using a throw-away
// RSA key. The test server ignores signature headers, so no real credentials
// are needed. The server is closed automatically via t.Cleanup.
func newDiscoveryTestClient(t *testing.T, handler http.Handler) *KalshiClient {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA test key: %v", err)
	}

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return &KalshiClient{
		httpClient:     srv.Client(),
		baseURL:        srv.URL,
		apiKeyID:       "test-key-id",
		privateKey:     key,
		log:            logger.New(io.Discard),
		seriesCache:    newSeriesCache(60 * time.Second),
		rateLimiter:    rate.NewLimiter(rate.Inf, 1),
		retryBaseDelay: 0,
	}
}

// serveByPath routes requests to seriesHandler when the path starts with
// "/series" and to eventsHandler for all other paths. Used to test the
// two-phase series-targeted search without a full mock exchange.
func serveByPath(seriesHandler, eventsHandler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/series") {
			seriesHandler.ServeHTTP(w, r)
		} else {
			eventsHandler.ServeHTTP(w, r)
		}
	})
}

// servePages returns a handler that serves successive pages from responses.
// Once all pages are exhausted it returns an empty event list with no cursor,
// which terminates the pagination loop cleanly.
func servePages(t *testing.T, pages []KalshiEventsResponse) http.Handler {
	t.Helper()
	var call int
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var resp KalshiEventsResponse
		if call < len(pages) {
			resp = pages[call]
		}
		call++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

// ── eventMatchesDiscoveryQuery unit tests ─────────────────────────────────────

func TestEventMatchesDiscoveryQuery(t *testing.T) {
	fedEvent := KalshiEvent{
		EventTicker: "FED-1",
		Title:       "Will the Fed cut rates in May?",
		Markets: []KalshiMarket{
			{Ticker: "FED-1-T500", YesSubTitle: "Greater than 5.00%"},
			{Ticker: "FED-1-T475", YesSubTitle: "Greater than 4.75%"},
		},
	}
	unrelatedEvent := KalshiEvent{
		EventTicker: "BTC-1",
		Title:       "Bitcoin above 100k",
		Markets: []KalshiMarket{
			{Ticker: "BTC-1-T1", YesSubTitle: "Yes, above $100,000"},
		},
	}
	solanaEvent := KalshiEvent{
		EventTicker: "SOL-25MAR",
		Title:       "Solana (SOL) Price at End of March 2025",
		Markets: []KalshiMarket{
			{Ticker: "SOL-25MAR-250", YesSubTitle: "Above $250"},
			{Ticker: "SOL-25MAR-200", YesSubTitle: "Above $200"},
		},
	}

	cases := []struct {
		name    string
		event   KalshiEvent
		query   string
		want    bool
	}{
		{"empty query matches everything", fedEvent, "", true},
		{"empty query on unrelated event", unrelatedEvent, "", true},
		{"layer1 title match case-insensitive", fedEvent, "fed", true},
		{"layer1 title match mixed case", fedEvent, "FED", true},
		{"layer1 title match substring", fedEvent, "cut rates", true},
		{"layer2 subtitle match", fedEvent, "5.00%", true},
		{"layer2 subtitle match case-insensitive", fedEvent, "greater than", true},
		{"no match in title or subtitle", fedEvent, "bitcoin", false},
		{"no match against unrelated event", unrelatedEvent, "fed", false},

		// Multi-word token matching (AND semantics)
		{"multi-word all tokens in title", fedEvent, "fed cut rates", true},
		{"multi-word partial miss", fedEvent, "fed bitcoin", false},
		{"multi-word tokens across title and subtitle", fedEvent, "fed 5.00%", true},
		{"multi-word order independent", fedEvent, "rates fed", true},
		{"multi-word with extra whitespace", fedEvent, "  fed   rates  ", true},

		// Natural-language query: stopwords ("of") may not appear in event title.
		{"natural language: Price of Solana end of march", solanaEvent, "Price of Solana end of march", true},
		{"natural language: solana price march (no stopwords)", solanaEvent, "solana price march", true},
		{"natural language: wrong topic", solanaEvent, "bitcoin price", false},
		{"query only stopwords matches any event", unrelatedEvent, "the of and in", true},
		// Kalshi often uses "SOL" in titles; synonym allows "Solana" query to match.
		{"SOL title matches Solana query", KalshiEvent{EventTicker: "KXSOLE-1", Title: "SOL price range March 31, 2025", Markets: nil}, "Price of Solana end of march", true},

		// Kalshi event titles say "Will X win Best Actor?" (verb form). A user
		// query containing "winner" or "winners" must match via synonym → "win"/"wins".
		{"winner synonym matches win in title", KalshiEvent{
			EventTicker: "KXOSCARACTO-26",
			Title:       "Will X win Best Actor at the 2026 Oscars?",
			Markets:     []KalshiMarket{{YesSubTitle: "Timothée Chalamet"}},
		}, "Oscars 2026: Best Actor Winner", true},

		// Punctuation attached to tokens must be stripped. "Oscars 2026: Best Actor Winner"
		// splits on whitespace into ["oscars", "2026:", "best", "actor", "winner"]. The
		// token "2026:" (with colon) would never appear verbatim in any event title, so
		// AND semantics would incorrectly reject every event. Punctuation stripping must
		// convert "2026:" → "2026" before matching.
		{"trailing colon stripped from year token", KalshiEvent{
			EventTicker: "KXOSCARSUPACTO-26",
			Title:       "Will the 2026 Oscars Best Actor winner be X?",
			Markets:     []KalshiMarket{{YesSubTitle: "actor winner nominee"}},
		}, "Oscars 2026: Best Actor Winner", true},

		// Year tokens ("2026") are OPTIONAL in event text matching. Award market
		// titles like "Will X win Best Actor at the Oscars?" do not include the
		// year, but they are still the correct result for "Oscars 2026: Best Actor
		// Winner". The full-catalogue scan already excludes past markets via
		// min_close_ts, so omitting the year check here does not admit stale data.
		{"year token optional — award event without year matches 2026 query", KalshiEvent{
			EventTicker: "KXOSCARBEST-26",
			Title:       "Will X win Best Actor at the Oscars?",
			Markets:     []KalshiMarket{{YesSubTitle: "Cillian Murphy"}},
		}, "Oscars 2026: Best Actor Winner", true},

		// "oscars" (plural) in the query must match Kalshi event text that uses the
		// singular "oscar". The substring direction is asymmetric: "oscar" IS a
		// substring of "oscars" (so singular-query → plural-title works), but "oscars"
		// is NOT a substring of "oscar" (so plural-query → singular-title requires the
		// explicit synonym entry "oscars" → ["oscar"]).
		{"oscars plural query matches oscar singular event title", KalshiEvent{
			EventTicker: "KXOSCARBEST-26B",
			Title:       "Will X win the Oscar for Best Actor?",
			Markets:     []KalshiMarket{{YesSubTitle: "Adrien Brody"}},
		}, "Oscars 2026: Best Actor Winner", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := eventMatchesDiscoveryQuery(tc.event, tc.query)
			if got != tc.want {
				t.Errorf("eventMatchesDiscoveryQuery(%q, %q) = %v, want %v",
					tc.event.EventTicker, tc.query, got, tc.want)
			}
		})
	}
}

func TestEventMatchesDiscoveryQuery_MultiWordPolitics(t *testing.T) {
	event := KalshiEvent{
		EventTicker: "HOUSE-26",
		Title:       "Will Democrats control the House in 2026",
		Markets: []KalshiMarket{
			{Ticker: "HOUSE-26-YES", YesSubTitle: "Democrats control"},
		},
	}

	cases := []struct {
		name  string
		query string
		want  bool
	}{
		{"all tokens present in title", "democrats house 2026", true},
		{"tokens in different order", "2026 house democrats", true},
		{"one token missing", "democrats senate 2026", false},
		{"single token match", "democrats", true},
		{"case insensitive multi-word", "DEMOCRATS HOUSE", true},
		{"token in subtitle only", "democrats control house", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := eventMatchesDiscoveryQuery(event, tc.query)
			if got != tc.want {
				t.Errorf("eventMatchesDiscoveryQuery(%q, %q) = %v, want %v",
					event.EventTicker, tc.query, got, tc.want)
			}
		})
	}
}

// ── SearchAllOpenEvents integration tests ────────────────────────────────────

func TestSearchAllOpenEvents_EmptyQueryMatchesAll(t *testing.T) {
	events := []KalshiEvent{
		{EventTicker: "ALPHA-1", Title: "Something Alpha"},
		{EventTicker: "BETA-1", Title: "Something Beta"},
	}
	client := newDiscoveryTestClient(t, servePages(t, []KalshiEventsResponse{
		{Events: events, Cursor: ""},
	}))

	got, err := client.SearchAllOpenEvents(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("empty query: want 2 events, got %d", len(got))
	}
}

func TestSearchAllOpenEvents_Layer1TitleMatch(t *testing.T) {
	events := []KalshiEvent{
		{EventTicker: "FED-1", Title: "Will the Fed cut rates in May?"},
		{EventTicker: "BTC-1", Title: "Bitcoin price above 100k"},
	}
	client := newDiscoveryTestClient(t, servePages(t, []KalshiEventsResponse{
		{Events: events, Cursor: ""},
	}))

	got, err := client.SearchAllOpenEvents(context.Background(), "Fed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("layer1: want 1 match, got %d", len(got))
	}
	if got[0].EventTicker != "FED-1" {
		t.Errorf("layer1: want FED-1, got %s", got[0].EventTicker)
	}
}

func TestSearchAllOpenEvents_Layer2SubtitleMatch(t *testing.T) {
	// Event title is too generic to contain the query; the detail lives in the subtitle.
	events := []KalshiEvent{
		{
			EventTicker: "FEDRATE-1",
			Title:       "Fed target rate after May meeting", // does NOT contain "5.00%"
			Markets: []KalshiMarket{
				{Ticker: "FEDRATE-1-T500", YesSubTitle: "Greater than 5.00%"},
				{Ticker: "FEDRATE-1-T475", YesSubTitle: "Greater than 4.75%"},
			},
		},
		{
			EventTicker: "IRRELEVANT-1",
			Title:       "Some unrelated event",
			Markets: []KalshiMarket{
				{Ticker: "IRREL-1-T1", YesSubTitle: "Unrelated subtitle"},
			},
		},
	}
	client := newDiscoveryTestClient(t, servePages(t, []KalshiEventsResponse{
		{Events: events, Cursor: ""},
	}))

	got, err := client.SearchAllOpenEvents(context.Background(), "5.00%")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("layer2: want 1 match, got %d", len(got))
	}
	if got[0].EventTicker != "FEDRATE-1" {
		t.Errorf("layer2: want FEDRATE-1, got %s", got[0].EventTicker)
	}
}

func TestSearchAllOpenEvents_NoMatch(t *testing.T) {
	events := []KalshiEvent{
		{EventTicker: "BTC-1", Title: "Bitcoin price above 100k"},
	}
	client := newDiscoveryTestClient(t, servePages(t, []KalshiEventsResponse{
		{Events: events, Cursor: ""},
	}))

	got, err := client.SearchAllOpenEvents(context.Background(), "weather")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("no-match: want 0 results, got %d", len(got))
	}
}

func TestSearchAllOpenEvents_PaginationCollectsAcrossAllPages(t *testing.T) {
	// Three-page catalogue; Fed events appear on pages 1 and 3, not page 2.
	pages := []KalshiEventsResponse{
		{
			Events: []KalshiEvent{{EventTicker: "FED-1", Title: "Fed rate decision January"}},
			Cursor: "cursor-page2",
		},
		{
			Events: []KalshiEvent{{EventTicker: "BTC-1", Title: "Bitcoin above 100k"}},
			Cursor: "cursor-page3",
		},
		{
			Events: []KalshiEvent{{EventTicker: "FED-2", Title: "Fed rate decision May"}},
			Cursor: "", // final page
		},
	}
	client := newDiscoveryTestClient(t, servePages(t, pages))

	got, err := client.SearchAllOpenEvents(context.Background(), "Fed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("pagination: want 2 Fed matches across 3 pages, got %d", len(got))
	}
	tickers := map[string]bool{got[0].EventTicker: true, got[1].EventTicker: true}
	if !tickers["FED-1"] || !tickers["FED-2"] {
		t.Errorf("pagination: want FED-1 and FED-2, got %v", tickers)
	}
}

func TestSearchAllOpenEvents_CursorForwardedOnSubsequentRequests(t *testing.T) {
	// Verify that the cursor value returned by page N is sent as the cursor
	// query parameter on page N+1, and that the first request carries no cursor.
	var receivedCursors []string

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCursors = append(receivedCursors, r.URL.Query().Get("cursor"))

		var resp KalshiEventsResponse
		if len(receivedCursors) == 1 {
			// First page: return a non-empty cursor to trigger a second request.
			resp = KalshiEventsResponse{
				Events: []KalshiEvent{{EventTicker: "E1", Title: "event one"}},
				Cursor: "next-cursor",
			}
		}
		// Second request: empty cursor in response → pagination stops.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	client := newDiscoveryTestClient(t, handler)
	_, err := client.SearchAllOpenEvents(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(receivedCursors) != 2 {
		t.Fatalf("cursor forwarding: want 2 requests, got %d", len(receivedCursors))
	}
	if receivedCursors[0] != "" {
		t.Errorf("cursor forwarding: first request should carry no cursor, got %q", receivedCursors[0])
	}
	if receivedCursors[1] != "next-cursor" {
		t.Errorf("cursor forwarding: second request should carry %q, got %q",
			"next-cursor", receivedCursors[1])
	}
}

func TestSearchAllOpenEvents_ContextCancelledBetweenPages(t *testing.T) {
	// The server cancels the context while serving page 1. The pagination
	// loop should detect the cancellation during the rate-limit delay (before
	// requesting page 2) and return an error.
	ctx, cancel := context.WithCancel(context.Background())

	calls := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			resp := KalshiEventsResponse{
				Events: []KalshiEvent{{EventTicker: "E1", Title: "event"}},
				Cursor: "cursor-p2",
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			// Cancel after page 1 is fully written; the select in the loop
			// will pick ctx.Done() before time.After(50ms) fires.
			cancel()
			return
		}
		t.Errorf("unexpected request to page %d after context cancellation", calls)
		w.WriteHeader(http.StatusInternalServerError)
	})

	client := newDiscoveryTestClient(t, handler)
	_, err := client.SearchAllOpenEvents(ctx, "")
	if err == nil {
		t.Fatal("context cancellation: expected error, got nil")
	}
}

func TestSearchAllOpenEvents_NonOKStatusReturnsError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	client := newDiscoveryTestClient(t, handler)

	_, err := client.SearchAllOpenEvents(context.Background(), "anything")
	if err == nil {
		t.Fatal("non-200: expected error, got nil")
	}
}

func TestSearchAllOpenEvents_MalformedJSONReturnsError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not valid json`))
	})
	client := newDiscoveryTestClient(t, handler)

	_, err := client.SearchAllOpenEvents(context.Background(), "anything")
	if err == nil {
		t.Fatal("malformed JSON: expected error, got nil")
	}
}

// ── WarmSeriesCache ───────────────────────────────────────────────────────────

func TestWarmSeriesCache_PopulatesCacheFromAPI(t *testing.T) {
	seriesResp := KalshiSeriesResponse{
		Series: []KalshiSeries{
			{Ticker: "KXBTCD", Title: "Bitcoin Daily Close", Tags: []string{"bitcoin", "btc"}},
			{Ticker: "KXFED", Title: "Fed Rate Decision", Tags: []string{"federal-reserve"}},
		},
	}
	seriesHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(seriesResp)
	})
	eventsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(KalshiEventsResponse{})
	})

	client := newDiscoveryTestClient(t, serveByPath(seriesHandler, eventsHandler))

	if err := client.WarmSeriesCache(context.Background()); err != nil {
		t.Fatalf("WarmSeriesCache: %v", err)
	}
	if !client.seriesCache.IsWarm() {
		t.Error("cache should be warm after WarmSeriesCache")
	}
	got := client.seriesCache.RelevantSeries("")
	if len(got) != 2 {
		t.Errorf("want 2 series in cache, got %d", len(got))
	}
}

func TestWarmSeriesCache_NonOKStatusReturnsError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	})
	client := newDiscoveryTestClient(t, handler)

	err := client.WarmSeriesCache(context.Background())
	if err == nil {
		t.Fatal("non-200: expected error, got nil")
	}
	if client.seriesCache.IsWarm() {
		t.Error("cache should remain cold after failed warm")
	}
}

func TestWarmSeriesCache_MalformedJSONReturnsError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not valid`))
	})
	client := newDiscoveryTestClient(t, handler)

	err := client.WarmSeriesCache(context.Background())
	if err == nil {
		t.Fatal("malformed JSON: expected error, got nil")
	}
}

// ── searchEventsBySeries ──────────────────────────────────────────────────────

func TestSearchEventsBySeries_FetchesOnlyRelevantSeries(t *testing.T) {
	// Series index: KXBTCD is relevant for "bitcoin"; KXFED is not.
	seriesResp := KalshiSeriesResponse{
		Series: []KalshiSeries{
			{Ticker: "KXBTCD", Title: "Bitcoin Daily Close", Tags: []string{"bitcoin", "btc"}},
			{Ticker: "KXFED", Title: "Fed Rate Decision", Tags: []string{"federal-reserve"}},
		},
	}
	btcEvent := KalshiEvent{
		EventTicker: "KXBTCD-26MAR",
		Title:       "Bitcoin price March 2026",
		Markets: []KalshiMarket{{
			Ticker:        "KXBTCD-26MAR-B95000",
			YesSubTitle:   "Above $95,000",
			YesBidDollars: "0.45",
			YesAskDollars: "0.47",
			CloseTime:     "2026-03-31T00:00:00Z",
			Status:        "active",
			OpenInterest:  500,
			Volume24h:     200,
		}},
	}

	var mu sync.Mutex
	var servedTickers []string

	eventsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ticker := r.URL.Query().Get("series_ticker")
		mu.Lock()
		servedTickers = append(servedTickers, ticker)
		mu.Unlock()

		resp := KalshiEventsResponse{}
		if ticker == "KXBTCD" {
			resp.Events = []KalshiEvent{btcEvent}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	seriesHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(seriesResp)
	})

	client := newDiscoveryTestClient(t, serveByPath(seriesHandler, eventsHandler))
	if err := client.WarmSeriesCache(context.Background()); err != nil {
		t.Fatalf("WarmSeriesCache: %v", err)
	}

	events, err := client.searchEventsBySeries(context.Background(), "bitcoin")
	if err != nil {
		t.Fatalf("searchEventsBySeries: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(servedTickers) != 1 || servedTickers[0] != "KXBTCD" {
		t.Errorf("want only KXBTCD fetched, got %v", servedTickers)
	}
	if len(events) != 1 || events[0].EventTicker != "KXBTCD-26MAR" {
		t.Errorf("want 1 bitcoin event, got %v", events)
	}
}

func TestSearchEventsBySeries_EmptyQueryFetchesAllSeries(t *testing.T) {
	seriesResp := KalshiSeriesResponse{
		Series: []KalshiSeries{
			{Ticker: "KXBTCD", Title: "Bitcoin Daily Close", Tags: []string{"bitcoin"}},
			{Ticker: "KXFED", Title: "Fed Rate Decision", Tags: []string{"federal-reserve"}},
		},
	}

	var mu sync.Mutex
	var servedTickers []string

	eventsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ticker := r.URL.Query().Get("series_ticker")
		mu.Lock()
		servedTickers = append(servedTickers, ticker)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(KalshiEventsResponse{})
	})
	seriesHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(seriesResp)
	})

	client := newDiscoveryTestClient(t, serveByPath(seriesHandler, eventsHandler))
	if err := client.WarmSeriesCache(context.Background()); err != nil {
		t.Fatalf("WarmSeriesCache: %v", err)
	}

	_, err := client.searchEventsBySeries(context.Background(), "")
	if err != nil {
		t.Fatalf("searchEventsBySeries: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(servedTickers) != 2 {
		t.Errorf("empty query: want 2 series fetched, got %d (%v)", len(servedTickers), servedTickers)
	}
}

func TestSearchEventsBySeries_ReturnsAllEventsFromMatchedSeries(t *testing.T) {
	// Both events belong to the bitcoin series. The series-targeted path must
	// return ALL events from matched series without per-event token filtering:
	// precision is the series scorer's job; recall is this layer's job.
	seriesResp := KalshiSeriesResponse{
		Series: []KalshiSeries{
			{Ticker: "KXBTCD", Title: "Bitcoin Daily Close", Tags: []string{"bitcoin"}},
		},
	}
	eventA := KalshiEvent{
		EventTicker: "KXBTCD-26MAR",
		Title:       "Bitcoin price March",
		Markets:     []KalshiMarket{{Ticker: "T1", YesSubTitle: "Above $95k"}},
	}
	eventB := KalshiEvent{
		EventTicker: "KXBTCD-26APR",
		Title:       "Bitcoin price April",
		Markets:     []KalshiMarket{{Ticker: "T2", YesSubTitle: "Below $90k"}},
	}

	eventsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(KalshiEventsResponse{
			Events: []KalshiEvent{eventA, eventB},
		})
	})
	seriesHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(seriesResp)
	})

	client := newDiscoveryTestClient(t, serveByPath(seriesHandler, eventsHandler))
	if err := client.WarmSeriesCache(context.Background()); err != nil {
		t.Fatalf("WarmSeriesCache: %v", err)
	}

	// Even though "above" only appears in eventA's subtitle, both events must
	// be returned — the event filter no longer applies on the series-targeted path.
	events, err := client.searchEventsBySeries(context.Background(), "bitcoin above")
	if err != nil {
		t.Fatalf("searchEventsBySeries: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("series-targeted path: want 2 events (all from series), got %d: %v",
			len(events), events)
	}
}

func TestSearchEventsBySeries_ColdCacheReturnsEmpty(t *testing.T) {
	// When the series cache is cold, searchEventsBySeries returns nothing
	// rather than panic or error — FetchMarkets falls back in this case.
	client := newDiscoveryTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no HTTP requests should be made when cache is cold")
	}))

	events, err := client.searchEventsBySeries(context.Background(), "bitcoin")
	if err != nil {
		t.Fatalf("cold cache: unexpected error: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("cold cache: want 0 events, got %d", len(events))
	}
}
