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
	defaultLimit       = 10
	searchPath         = "/v1/search"
	polymarketSearchPath = "/v1/polymarket/search"
	matchesPathPrefix  = "/v1/matches/"
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

// ─── Polymarket search types ──────────────────────────────────────────────────

// PolymarketSearchResult is one event returned by GET /v1/polymarket/search.
type PolymarketSearchResult struct {
	EventID       string   `json:"event_id"`
	Slug          string   `json:"slug"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	EventType     string   `json:"event_type"`
	TagLabels     []string `json:"tag_labels"`
	MarketCount   int      `json:"market_count"`
	EndDate       string   `json:"end_date"`
	SearchScore   float64  `json:"search_score"`
	MatchSource   string   `json:"match_source"`
	PolymarketURL string   `json:"polymarket_url"`
	ClobURL       string   `json:"clob_url"`
}

// PolymarketSearchMeta is the meta object in the polymarket search response.
type PolymarketSearchMeta struct {
	TotalResults int  `json:"total_results"`
	QueryTimeMs  int  `json:"query_time_ms"`
	Cached       bool `json:"cached"`
}

// PolymarketSearchResponse is the top-level response from GET /v1/polymarket/search.
type PolymarketSearchResponse struct {
	Query   string                   `json:"query"`
	Results []PolymarketSearchResult `json:"results"`
	Meta    PolymarketSearchMeta     `json:"meta"`
}

// ─── Cross-platform match types ───────────────────────────────────────────────

// MatchEntry is one pre-computed cross-platform link returned by
// GET /v1/matches/{kalshi_ticker}.
type MatchEntry struct {
	KalshiTicker string   `json:"kalshi_ticker"`
	PolyEventID  string   `json:"poly_event_id"`
	MatchType    string   `json:"match_type"`
	Confidence   float64  `json:"confidence"`
	EntityBasis  []string `json:"entity_basis"`
	VectorScore  float64  `json:"vector_score"`
	EntityScore  float64  `json:"entity_score"`
	KalshiTitle  string   `json:"kalshi_title"`
	PolyTitle    string   `json:"poly_title"`
	KalshiURL    string   `json:"kalshi_url"`
	PolyClobURL  string   `json:"poly_clob_url"`
	EventType    string   `json:"event_type"`
	EntityDate   string   `json:"entity_date"`
	KalshiCloses string   `json:"kalshi_closes"`
	PolyCloses   string   `json:"poly_closes"`
}

// MatchMeta is the meta object in the matches response.
type MatchMeta struct {
	TotalMatches  int     `json:"total_matches"`
	MinConfidence float64 `json:"min_confidence"`
}

// MatchResponse is the top-level response from GET /v1/matches/{kalshi_ticker}.
type MatchResponse struct {
	KalshiTicker string       `json:"kalshi_ticker"`
	Matches      []MatchEntry `json:"matches"`
	Meta         MatchMeta    `json:"meta"`
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

// SearchPolymarket runs GET /v1/polymarket/search?q={query}&limit={limit}.
// limit is capped at 50 per the API contract.
// Returns an empty result set (not an error) when no events match.
func (c *Client) SearchPolymarket(ctx context.Context, query string, limit int) (*PolymarketSearchResponse, error) {
	if strings.TrimSpace(query) == "" {
		return &PolymarketSearchResponse{Query: query}, nil
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > 50 {
		limit = 50
	}

	params := url.Values{}
	params.Set("q", query)
	params.Set("limit", fmt.Sprintf("%d", limit))
	fullURL := c.baseURL + polymarketSearchPath + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshidb",
			Message: "failed to build polymarket search request",
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
			Message: "polymarket search HTTP request failed",
			Err:     err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.log.Warn("connector", "kalshidb",
			fmt.Sprintf("polymarket search returned status %d for query=%q", resp.StatusCode, query))
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshidb",
			Message: fmt.Sprintf("polymarket search API returned status %d", resp.StatusCode),
			Err:     fmt.Errorf("status %d", resp.StatusCode),
		}
	}

	var out PolymarketSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		c.log.Warn("connector", "kalshidb",
			fmt.Sprintf("failed to decode polymarket search response for query=%q: %v", query, err))
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshidb",
			Message: "failed to decode polymarket search response",
			Err:     err,
		}
	}

	c.log.Info("connector", "kalshidb",
		fmt.Sprintf("polymarket search query=%q returned %d results", query, len(out.Results)))
	return &out, nil
}

// GetMatches runs GET /v1/matches/{kalshiTicker}?min_confidence={minConfidence}.
// Returns an empty slice (not an error) when no matches exist for the ticker.
// A non-200 response or decode error is returned as an error.
func (c *Client) GetMatches(ctx context.Context, kalshiTicker string, minConfidence float64) ([]MatchEntry, error) {
	if kalshiTicker == "" {
		return nil, nil
	}

	params := url.Values{}
	params.Set("min_confidence", fmt.Sprintf("%.2f", minConfidence))
	fullURL := c.baseURL + matchesPathPrefix + url.PathEscape(kalshiTicker) + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshidb",
			Message: fmt.Sprintf("failed to build matches request for ticker=%s", kalshiTicker),
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
			Message: fmt.Sprintf("matches HTTP request failed for ticker=%s", kalshiTicker),
			Err:     err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.log.Warn("connector", "kalshidb",
			fmt.Sprintf("matches API returned status %d for ticker=%s", resp.StatusCode, kalshiTicker))
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshidb",
			Message: fmt.Sprintf("matches API returned status %d for ticker=%s", resp.StatusCode, kalshiTicker),
			Err:     fmt.Errorf("status %d", resp.StatusCode),
		}
	}

	var out MatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		c.log.Warn("connector", "kalshidb",
			fmt.Sprintf("failed to decode matches response for ticker=%s: %v", kalshiTicker, err))
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshidb",
			Message: fmt.Sprintf("failed to decode matches response for ticker=%s", kalshiTicker),
			Err:     err,
		}
	}

	if len(out.Matches) > 0 {
		c.log.Info("connector", "kalshidb",
			fmt.Sprintf("matches ticker=%s → %d match(es) (best: %s %.2f)",
				kalshiTicker, len(out.Matches), out.Matches[0].MatchType, out.Matches[0].Confidence))
	}

	return out.Matches, nil
}
