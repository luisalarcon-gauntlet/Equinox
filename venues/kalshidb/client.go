package kalshidb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	equinoxerrors "github.com/equinox/errors"
	"github.com/equinox/logger"
)

const (
	defaultLimit = 10
	searchPath   = "/v1/search"
)

// SearchResult is one event returned by GET /v1/search.
type SearchResult struct {
	EventTicker  string  `json:"event_ticker"`
	SeriesTicker string  `json:"series_ticker"`
	Title        string  `json:"title"`
	SubTitle     string  `json:"sub_title"`
	Category     string  `json:"category"`
	EventType    string  `json:"event_type"`
	MarketCount  int     `json:"market_count"`
	CloseTime    string  `json:"close_time"`
	SearchScore  float64 `json:"search_score"`
	MatchSource  string  `json:"match_source"`
	KalshiURL    string  `json:"kalshi_url"`
}

// SearchMeta is the meta object in the search response.
type SearchMeta struct {
	TotalResults int  `json:"total_results"`
	QueryTimeMs  int  `json:"query_time_ms"`
	Cached       bool `json:"cached"`
}

// SearchResponse is the top-level response from GET /v1/search.
type SearchResponse struct {
	Query   string         `json:"query"`
	Results []SearchResult `json:"results"`
	Meta    SearchMeta     `json:"meta"`
}

// Client calls the KalshiDB search API. All /v1/ endpoints require X-API-Key.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
	log        *logger.Logger
}

// NewClient builds a KalshiDB client. apiKey is required for Search.
func NewClient(baseURL, apiKey string, timeout time.Duration, log *logger.Logger) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 20 * time.Second,
			},
		},
		baseURL: baseURL,
		apiKey:  apiKey,
		log:     log,
	}
}

// Search runs GET /v1/search?q={query}&limit={limit} with X-API-Key.
// limit is capped at 20 per the API; use 10 per plan.
func (c *Client) Search(ctx context.Context, query string, limit int) (*SearchResponse, error) {
	if strings.TrimSpace(query) == "" {
		return &SearchResponse{Query: query, Results: nil, Meta: SearchMeta{}}, nil
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > 20 {
		limit = 20
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", fmt.Sprintf("%d", limit))
	fullURL := c.baseURL + searchPath + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshidb",
			Message: "failed to build search request",
			Err:     err,
		}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshidb",
			Message: "search HTTP request failed",
			Err:     err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshidb",
			Message: fmt.Sprintf("search API returned status %d", resp.StatusCode),
			Err:     fmt.Errorf("status %d", resp.StatusCode),
		}
	}

	var out SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshidb",
			Message: "failed to decode search response",
			Err:     err,
		}
	}
	return &out, nil
}
