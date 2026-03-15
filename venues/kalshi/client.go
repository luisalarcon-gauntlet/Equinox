package kalshi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/equinox/config"
	equinoxerrors "github.com/equinox/errors"
	"github.com/equinox/logger"
	"github.com/equinox/models"
	"github.com/equinox/trace"
	"github.com/equinox/venues"
	"github.com/equinox/venues/kalshidb"
)

const (
	defaultSearchPageSize = 100
	maxSearchPages        = 3

	// degeneratePriceThreshold is the distance from 0 or 1 at which a market's
	// mid price is considered effectively decided.
	degeneratePriceThreshold = 0.03

	// maxRetries is the number of times doGet will retry a 429 response before
	// giving up. Total attempts = 1 (initial) + maxRetries.
	maxRetries = 3
)

// KalshiClient fetches market data from Kalshi's unauthenticated v1 search API
// and adapts nested search results into canonical models.Market values.
// When kalshidb is set, FetchMarkets tries KalshiDB first and falls back to v1 search.
type KalshiClient struct {
	httpClient           *http.Client
	baseURL              string
	log                  *logger.Logger
	rateLimiter          *rate.Limiter
	retryBaseDelay       time.Duration
	kalshidb             *kalshidb.Client
	matchesMinConfidence float64
}

// NewKalshiClient constructs a v1 Kalshi client. Read endpoints are
// unauthenticated, so no key material is required.
// If cfg.KalshiDBAPIKey is set, the client will use KalshiDB for search first
// and enrich results with cross-venue match data.
func NewKalshiClient(cfg *config.Config, log *logger.Logger) (*KalshiClient, error) {
	c := &KalshiClient{
		httpClient: &http.Client{
			Timeout: cfg.HTTPTimeout,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 20 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		baseURL:              strings.TrimRight(cfg.KalshiBaseURL, "/"),
		log:                  log,
		rateLimiter:          rate.NewLimiter(rate.Every(60*time.Millisecond), 3),
		retryBaseDelay:       500 * time.Millisecond,
		matchesMinConfidence: cfg.MatchesMinConfidence,
	}
	if cfg.KalshiDBAPIKey != "" {
		c.kalshidb = kalshidb.NewClient(cfg.KalshiDBBaseURL, cfg.KalshiDBAPIKey, cfg.HTTPTimeout, log)
	}
	return c, nil
}

// GetVenueName satisfies the VenueConnector interface.
func (c *KalshiClient) GetVenueName() string { return "kalshi" }

// FetchMarkets returns markets matching the query. When KalshiDB is configured,
// it searches KalshiDB first (limit 10 events), enriches each via kalshi_url for
// live prices, then returns those markets and logs source=kalshidb. If KalshiDB
// returns nothing or all event fetches fail, it falls back to v1 search/series
// and logs source=search.
func (c *KalshiClient) FetchMarkets(ctx context.Context, query string) ([]models.Market, error) {
	if strings.TrimSpace(query) == "" {
		return []models.Market{}, nil
	}

	if c.kalshidb != nil {
		markets, eventsUsed := c.fetchMarketsViaKalshiDB(ctx, query)
		if len(markets) > 0 {
			c.log.Info("connector", "kalshi",
				fmt.Sprintf("kalshi source=kalshidb query=%q events=%d markets=%d", query, eventsUsed, len(markets)))
			return markets, nil
		}
		// Fallback to v1 search
	}

	markets, err := c.fetchMarketsViaSearchSeries(ctx, query)
	if err != nil {
		return nil, err
	}
	c.log.Info("connector", "kalshi",
		fmt.Sprintf("kalshi source=search query=%q markets=%d", query, len(markets)))
	return markets, nil
}

const kalshiDBSearchLimit = 10

// fetchMarketsViaKalshiDB searches KalshiDB (limit 10), fetches each event by
// kalshi_url for live prices, then concurrently looks up cross-venue matches and
// attaches CrossVenueURL/Confidence/MatchType to each market that has one.
func (c *KalshiClient) fetchMarketsViaKalshiDB(ctx context.Context, query string) ([]models.Market, int) {
	qt := trace.FromContext(ctx)

	sr, err := c.kalshidb.Search(ctx, query, kalshiDBSearchLimit)
	if err != nil {
		c.log.Warn("connector", "kalshi", fmt.Sprintf("KalshiDB search failed, falling back to search: %v", err))
		return nil, 0
	}
	if sr == nil || len(sr.Results) == 0 {
		return nil, 0
	}

	qt.Add(fmt.Sprintf("kalshi: kalshidb-search → %d events", len(sr.Results)))

	seenTickers := make(map[string]struct{})
	var markets []models.Market
	eventsUsed := 0

	// matchesByTicker holds the best match for each event ticker, populated
	// concurrently below after live price fetching is complete.
	type matchResult struct {
		ticker  string
		entries []kalshidb.MatchEntry
		err     error
	}

	// Collect tickers that we successfully adapt so we can look up matches.
	type adaptedEvent struct {
		ticker  string
		markets []models.Market
	}
	var adaptedEvents []adaptedEvent

	for _, res := range sr.Results {
		if res.KalshiURL == "" {
			continue
		}
		evResp, err := c.FetchEventByURL(ctx, res.KalshiURL)
		if err != nil {
			c.log.Warn("connector", "kalshi",
				fmt.Sprintf("fetch event %q failed: %v", res.EventTicker, err))
			continue
		}
		eventsUsed++
		eventMarkets, err := AdaptV2EventMarkets(evResp.Event)
		if err != nil {
			c.log.Warn("connector", "kalshi", fmt.Sprintf("adapt event %q: %v", evResp.Event.EventTicker, err))
			continue
		}

		var fresh []models.Market
		for _, m := range eventMarkets {
			if _, seen := seenTickers[m.VenueID]; seen {
				continue
			}
			seenTickers[m.VenueID] = struct{}{}
			fresh = append(fresh, m)
		}
		if len(fresh) > 0 {
			adaptedEvents = append(adaptedEvents, adaptedEvent{
				ticker:  res.EventTicker,
				markets: fresh,
			})
		}
	}

	// Concurrently fetch cross-venue matches for every adapted event.
	matchCh := make(chan matchResult, len(adaptedEvents))
	var wg sync.WaitGroup
	for _, ae := range adaptedEvents {
		wg.Add(1)
		go func(ticker string) {
			defer wg.Done()
			entries, err := c.kalshidb.GetMatches(ctx, ticker, c.matchesMinConfidence)
			matchCh <- matchResult{ticker: ticker, entries: entries, err: err}
		}(ae.ticker)
	}
	wg.Wait()
	close(matchCh)

	// Build a lookup: event ticker → best MatchEntry (first = highest confidence
	// because the API returns matches sorted by confidence descending).
	matchesByTicker := make(map[string]kalshidb.MatchEntry)
	for mr := range matchCh {
		if mr.err != nil {
			c.log.Warn("connector", "kalshi",
				fmt.Sprintf("GetMatches failed for ticker=%s: %v", mr.ticker, mr.err))
			continue
		}
		if len(mr.entries) > 0 {
			matchesByTicker[mr.ticker] = mr.entries[0]
			c.log.Info("connector", "kalshi",
				fmt.Sprintf("match ticker=%s → poly_event=%s confidence=%.2f type=%s",
					mr.ticker, mr.entries[0].PolyEventID,
					mr.entries[0].Confidence, mr.entries[0].MatchType))
		}
	}

	// Assemble the final market slice, attaching cross-venue data where available.
	matchedCount := 0
	bestConfidence := 0.0
	bestMatchType := ""

	for _, ae := range adaptedEvents {
		entry, hasMatch := matchesByTicker[ae.ticker]
		for _, m := range ae.markets {
			if hasMatch && entry.PolyClobURL != "" {
				m.CrossVenueURL = entry.PolyClobURL
				m.CrossVenueConfidence = entry.Confidence
				m.CrossVenueMatchType = entry.MatchType
			}
			markets = append(markets, m)
			if len(markets) >= venues.MaxMarketsPerVenue {
				goto done
			}
		}
		if hasMatch {
			matchedCount++
			if entry.Confidence > bestConfidence {
				bestConfidence = entry.Confidence
				bestMatchType = entry.MatchType
			}
		}
	}

done:
	c.log.Info("connector", "kalshi",
		fmt.Sprintf("enriched %d/%d events with cross-venue matches", matchedCount, len(adaptedEvents)))

	if matchedCount > 0 {
		qt.Add(fmt.Sprintf("kalshi: matches → %d matched (best: %s %.2f)", matchedCount, bestMatchType, bestConfidence))
	} else {
		qt.Add("kalshi: matches → none found")
	}

	return markets, eventsUsed
}

// fetchMarketsViaSearchSeries uses v1 search/series (existing implementation).
func (c *KalshiClient) fetchMarketsViaSearchSeries(ctx context.Context, query string) ([]models.Market, error) {
	params := SearchParams{
		Query:    query,
		PageSize: defaultSearchPageSize,
		OrderBy:  "querymatch",
		Status:   "open",
	}

	var (
		allResults []KalshiSeriesResult
		pageCount  int
	)

	for pageCount < maxSearchPages {
		resp, err := c.SearchSeries(ctx, params)
		if err != nil {
			return nil, err
		}
		pageCount++
		allResults = append(allResults, resp.CurrentPage...)
		if resp.NextCursor == "" {
			break
		}
		params.Cursor = resp.NextCursor
		params.Page = 0
	}

	seenTickers := make(map[string]struct{})
	markets := make([]models.Market, 0, len(allResults)*2)

	for _, result := range allResults {
		for _, leg := range result.Markets {
			if _, seen := seenTickers[leg.Ticker]; seen {
				continue
			}
			seenTickers[leg.Ticker] = struct{}{}

			if leg.Status != "" && mapKalshiStatus(leg.Status) != "open" {
				continue
			}

			if leg.Result != "" {
				continue
			}

			m, err := AdaptKalshiMarket(result, leg)
			if err != nil {
				c.log.Warn("connector", "kalshi",
					fmt.Sprintf("skipping market %q in event %q: %v", leg.Ticker, result.EventTicker, err))
				continue
			}

			if m.YesMid <= degeneratePriceThreshold || m.YesMid >= (1.0-degeneratePriceThreshold) {
				continue
			}

			markets = append(markets, m)
			if len(markets) >= venues.MaxMarketsPerVenue {
				return markets, nil
			}
		}
	}

	return markets, nil
}

// SearchSeries issues one GET /v1/search/series request.
func (c *KalshiClient) SearchSeries(ctx context.Context, params SearchParams) (*SearchResponse, error) {
	if strings.TrimSpace(params.Query) == "" {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshi",
			Message: "query must not be empty",
		}
	}

	fullURL := fmt.Sprintf("%s/v1/search/series?%s", c.baseURL, buildSearchQuery(params).Encode())
	resp, err := c.doGet(ctx, fullURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshi",
			Message: "failed to decode search response",
			Err:     err,
		}
	}
	return &result, nil
}

// FetchEventByURL fetches one event with nested markets from a full URL (e.g. from KalshiDB's kalshi_url).
// The URL should point at trade-api/v2/events/{event_ticker}?with_nested_markets=true.
// Returns live prices and market list for that event.
func (c *KalshiClient) FetchEventByURL(ctx context.Context, kalshiURL string) (*V2EventResponse, error) {
	if kalshiURL == "" {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshi",
			Message: "kalshi_url is empty",
		}
	}
	resp, err := c.doGet(ctx, kalshiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result V2EventResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshi",
			Message: "failed to decode v2 event response",
			Err:     err,
		}
	}
	return &result, nil
}

// GetSeries fetches full series metadata for one or more series tickers.
func (c *KalshiClient) GetSeries(ctx context.Context, tickers ...string) ([]SeriesDetail, error) {
	if len(tickers) == 0 {
		return []SeriesDetail{}, nil
	}

	q := url.Values{}
	q.Set("series_tickers", strings.Join(tickers, ","))
	fullURL := fmt.Sprintf("%s/v1/series/?%s", c.baseURL, q.Encode())

	resp, err := c.doGet(ctx, fullURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result SeriesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshi",
			Message: "failed to decode series response",
			Err:     err,
		}
	}
	return result.Series, nil
}

func buildSearchQuery(params SearchParams) url.Values {
	q := url.Values{}
	q.Set("query", params.Query)
	if params.PageSize > 0 {
		q.Set("page_size", strconv.Itoa(params.PageSize))
	}
	if params.Page > 1 {
		q.Set("page", strconv.Itoa(params.Page))
	}
	if params.Cursor != "" {
		q.Set("cursor", params.Cursor)
	}
	if params.OrderBy != "" {
		q.Set("order_by", params.OrderBy)
	}
	if params.Category != "" {
		q.Set("category", params.Category)
	}
	if params.Status != "" {
		q.Set("status", params.Status)
	}
	return q
}

// doGet executes a GET request to fullURL, checks the status code, and returns
// the raw *http.Response so callers can decode the body into their own type.
// The caller is responsible for closing resp.Body.
func (c *KalshiClient) doGet(ctx context.Context, fullURL string) (*http.Response, error) {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := c.rateLimiter.Wait(ctx); err != nil {
			return nil, &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: "rate limiter wait cancelled",
				Err:     err,
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return nil, &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: "failed to build HTTP request",
				Err:     err,
			}
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: "HTTP request failed",
				Err:     err,
			}
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			lastErr = &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: fmt.Sprintf("API returned status 429 after %d attempt(s)", attempt+1),
				Err:     fmt.Errorf("status 429"),
			}
			if attempt < maxRetries {
				delay := c.retryBackoff(attempt, resp.Header.Get("Retry-After"))
				select {
				case <-ctx.Done():
					return nil, &equinoxerrors.EquinoxError{
						Layer:   "connector",
						Venue:   "kalshi",
						Message: "context cancelled while waiting to retry after 429",
						Err:     ctx.Err(),
					}
				case <-time.After(delay):
				}
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: fmt.Sprintf("API returned status %d", resp.StatusCode),
				Err:     fmt.Errorf("status %d", resp.StatusCode),
			}
		}

		return resp, nil
	}

	return nil, lastErr
}

// retryBackoff returns the duration to wait before the next retry attempt.
func (c *KalshiClient) retryBackoff(attempt int, retryAfter string) time.Duration {
	if retryAfter != "" {
		if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return c.retryBaseDelay * (1 << attempt)
}
