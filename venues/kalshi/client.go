package kalshi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/equinox/config"
	equinoxerrors "github.com/equinox/errors"
	"github.com/equinox/logger"
	"github.com/equinox/models"
	"github.com/equinox/venues"
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
type KalshiClient struct {
	httpClient     *http.Client
	baseURL        string
	log            *logger.Logger
	rateLimiter    *rate.Limiter
	retryBaseDelay time.Duration
}

// NewKalshiClient constructs a v1 Kalshi client. Read endpoints are
// unauthenticated, so no key material is required.
func NewKalshiClient(cfg *config.Config, log *logger.Logger) (*KalshiClient, error) {
	return &KalshiClient{
		httpClient: &http.Client{
			Timeout: cfg.HTTPTimeout,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 20 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		baseURL:        strings.TrimRight(cfg.KalshiBaseURL, "/"),
		log:            log,
		rateLimiter:    rate.NewLimiter(rate.Every(60*time.Millisecond), 3),
		retryBaseDelay: 500 * time.Millisecond,
	}, nil
}

// GetVenueName satisfies the VenueConnector interface.
func (c *KalshiClient) GetVenueName() string { return "kalshi" }

// FetchMarkets queries /v1/search/series and adapts nested market results into
// canonical models.Market values. We fetch a few ranked result pages so broad
// queries still surface enough candidates for downstream matching.
func (c *KalshiClient) FetchMarkets(ctx context.Context, query string) ([]models.Market, error) {
	if strings.TrimSpace(query) == "" {
		return []models.Market{}, nil
	}

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
