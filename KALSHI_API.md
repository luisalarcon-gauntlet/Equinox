# Kalshi V1 API — Implementation Reference

> **Source:** Reverse-engineered from `kalshi.com` via browser network interception (March 2026).
> **Purpose:** Drop-in reference for implementing Kalshi search and market data in a Go application.

---

## Background

Kalshi exposes two API generations at `api.elections.kalshi.com`:

| Generation | Base path | Notes |
|---|---|---|
| **v1** (undocumented) | `/v1/` | Powers the `kalshi.com` frontend. Text search lives here. No auth required for read endpoints. |
| **v2** (public) | `/trade-api/v2/` | Officially documented at docs.kalshi.com. Has no working text search — the `search` param is silently ignored. |

All v1 read endpoints are **unauthenticated** and **CORS-open**. No API key or session cookie is required. The only reason the Node.js/Go app needs a proxy is to avoid browser CORS restrictions when serving from a non-Kalshi origin — the upstream requests themselves need no credentials.

---

## Base URL

```
https://api.elections.kalshi.com
```

All paths below are relative to this base.

---

## Endpoint Reference

### 1. `GET /v1/search/series` ★ Primary

The search endpoint powering the Kalshi search bar. Returns a ranked list of series (topic groups) matching the query, each with their nested active markets and live prices.

**Request**

```
GET /v1/search/series?query=bitcoin&page_size=5&order_by=querymatch
```

| Query param | Type | Required | Default | Description |
|---|---|---|---|---|
| `query` | `string` | **Yes** | — | Free-text search term |
| `page_size` | `int` | No | server default | Results per page |
| `page` | `int` | No | `1` | Page number (1-based) |
| `order_by` | `enum` | No | `querymatch` | Sort order — see valid values below |
| `category` | `string` | No | — | Filter by category name (e.g. `Crypto`, `Entertainment`, `Politics`, `Economics`) |
| `status` | `string` | No | — | Filter by market status (e.g. `open`) |

**Valid `order_by` values** (validated server-side as a strict enum — any other value returns HTTP 400):

```
querymatch   — relevance to the search query (default)
volume       — total trading volume
liquidity    — available liquidity
trending     — trending markets
closing      — closing soonest first
newest       — most recently created first
```

**Response**

```json
{
  "total_results_count": 39,
  "next_cursor": "CAI",
  "current_page": [
    {
      "series_ticker":        "KXBTCD",
      "series_title":         "Bitcoin price Above/below",
      "event_ticker":         "KXBTCD-26MAR1017",
      "event_subtitle":       "On Mar 10, 2026 at 5pm EDT",
      "event_title":          "Bitcoin price today at 5pm EDT?",
      "category":             "Crypto",
      "total_series_volume":  1318403817,
      "total_volume":         1481700,
      "total_market_count":   40,
      "active_market_count":  40,
      "search_score":         200,
      "is_trending":          false,
      "is_new":               false,
      "is_closing":           false,
      "is_price_delta":       false,
      "fee_type":             "quadratic",
      "fee_multiplier":       1,
      "tags":                 ["crypto", "bitcoin"],
      "topic_keywords":       ["bitcoin", "btc", "price"],
      "milestone_id":         "",
      "product_metadata": {
        "categories":         ["Crypto"],
        "subcategories":      { "Crypto": ["Bitcoin"] },
        "scope":              null,
        "promoted_milestone_id": ""
      },
      "product_metadata_derived": {},
      "markets": [
        {
          "ticker":                 "KXBTCD-26MAR1017-T80001",
          "yes_subtitle":           "Above $80,001",
          "no_subtitle":            "",
          "yes_bid":                45,
          "yes_ask":                46,
          "last_price":             45,
          "yes_bid_dollars":        "0.4500",
          "yes_ask_dollars":        "0.4600",
          "last_price_dollars":     "0.4500",
          "price_delta":            2,
          "volume":                 120400,
          "score":                  9200,
          "close_ts":               "2026-03-10T21:00:00Z",
          "expected_expiration_ts": "2026-03-10T21:00:00Z",
          "open_ts":                "2026-03-10T14:00:00Z",
          "result":                 "",
          "custom_strike":          { "Price": "$80,001" },
          "rulebook_variables":     { "image_link": "https://..." },
          "image_url_dark_mode":    "https://...",
          "image_url_light_mode":   "https://...",
          "background_color_dark_mode":  "#000000",
          "background_color_light_mode": "#ffffff",
          "image_scale":            100,
          "previous_price":         43,
          "previous_price_dollars": "0.4300"
        }
      ]
    }
  ]
}
```

**Notes on prices:** All `yes_bid`, `yes_ask`, `last_price` fields are in **cents (1–99)**. The `*_dollars` variants are the same value as a decimal string (e.g. `45` → `"0.4500"`). `price_delta` is in cents relative to the previous price.

**Pagination:** The response includes `next_cursor` (a base64 token). You can pass it as `cursor=<value>` on subsequent requests instead of using `page`. Both mechanisms work.

---

### 2. `GET /v1/series/`

Fetch full series metadata by ticker. More detailed than the search result — includes regulatory URLs, settlement sources, keyword lists, and templates.

**Request**

```
GET /v1/series/?series_tickers=KXBTC,KXOSCARDIR
```

| Query param | Type | Required | Description |
|---|---|---|---|
| `series_tickers` | `string` | **Yes** | Comma-separated series ticker list |

**Response**

```json
{
  "series": [
    {
      "ticker":                      "KXOSCARDIR",
      "title":                       "Oscar for Best Director",
      "category":                    "Entertainment",
      "frequency":                   "annual",
      "fee_type":                    "quadratic",
      "fee_multiplier":              1,
      "series_volume":               3334629,
      "contract_ticker":             "OSCARS",
      "contract_url":                "https://kalshi-public-docs.s3.amazonaws.com/regulatory/...",
      "event_phrase_template":       "${event}",
      "market_phrase_template":      "${market}",
      "market_description_yes_template": "...",
      "market_description_no_template":  "...",
      "forecast_unit":               0,
      "minimum_value":               null,
      "maximum_value":               null,
      "keywords":                    ["oscar", "academy award", "best director", "film"],
      "tags":                        ["entertainment", "awards"],
      "settlement_sources":          [{ "name": "Academy of Motion Picture Arts and Sciences", "url": "..." }],
      "additional_prohibitions":     ["..."],
      "product_metadata": {
        "about":                     "...",
        "scope":                     "...",
        "subcategories":             { "Entertainment": ["Awards", "Movies"] },
        "suggester_nickname":        ""
      }
    }
  ]
}
```

---

### 3. `GET /v1/structured_targets/`

Entity lookup — returns typed real-world entities (players, competitors, teams) associated with a series. Used by the frontend to render player images and stats in sports markets.

**Request**

```
GET /v1/structured_targets/?series_ticker=KXNBA&page_size=20
```

| Query param | Type | Required | Description |
|---|---|---|---|
| `series_ticker` | `string` | **Yes** | Series to look up entities for |
| `page_size` | `int` | **Yes** | Must be ≥ 1 (server validates `min` tag) |
| `cursor` | `string` | No | Pagination cursor from previous response |

**Response**

```json
{
  "cursor": "00049...",
  "structured_targets": [
    {
      "id":              "00007b77-b477-4399-9eef-a7760434ad33",
      "name":            "LeBron James",
      "type":            "basketball_player",
      "last_updated_ts": "2026-02-16T17:14:52Z",
      "details": {
        "first_name":  "LeBron",
        "last_name":   "James",
        "jersey":      "23",
        "position":    "F",
        "team_id":     "lakers",
        "league":      "NBA",
        "status":      "active",
        "abbreviation": "LJAMES"
      },
      "product_details": {
        "sportradar_id": "...",
        "image_url":     "https://..."
      }
    }
  ]
}
```

**Known `type` values:** `basketball_player`, `ufc_competitor` (others likely exist for other sports).

---

### 4. `GET /v1/exchange/status` and `GET /v1/cached/exchange/status`

Returns whether the exchange and trading are currently active.

```
GET /v1/exchange/status
GET /v1/cached/exchange/status   ← served from edge cache, faster
```

**Response** (identical schema):

```json
{
  "exchange_active":          true,
  "trading_active":           true,
  "immediate_funding_active": false
}
```

---

### 5. `GET /v1/live_data/count`

Returns the number of users currently active on the platform.

```
GET /v1/live_data/count
```

```json
{ "count": 105 }
```

---

### 6. `GET /v1/location`

Returns geo-IP data for the caller. Used by the frontend to enforce jurisdictional access rules.

```
GET /v1/location
```

```json
{
  "country_iso": "US",
  "state_iso":   "TX",
  "city_name":   "Austin",
  "postal_code": "78701"
}
```

---

### Authenticated-only endpoints (requires session cookie + CSRF header)

These exist in the v1 surface but return errors without a valid browser session. They are listed for completeness only — do not attempt to call them from a backend service without proper auth.

| Endpoint | Method | Description |
|---|---|---|
| `/v1/bff/*` | `POST` | Backend-for-frontend proxy; base for all credentialed frontend calls |
| `/v1/bff/feed` | `GET` | Real-time feed bootstrap |
| `/v1/ws` | `GET` | WebSocket upgrade for live market data |
| `/v1/users/{id}` | `GET` | User profile |
| `/v1/intercom/jwt` | `GET` | Intercom support chat JWT |

The authenticated client uses `axios` with `withCredentials: true` and sends a CSRF token in the header named `X-CSRFToken` (confirmed from JS bundle constant `CSRF_HEADER_NAME`).

---

## Go Implementation Guide

### Structs

```go
package kalshi

import "time"

// SearchResponse is the top-level response from GET /v1/search/series.
type SearchResponse struct {
    TotalResultsCount int      `json:"total_results_count"`
    NextCursor        string   `json:"next_cursor"`
    CurrentPage       []Series `json:"current_page"`
}

// Series is a topic group (e.g. "Bitcoin price Above/below") returned by search.
type Series struct {
    SeriesTicker        string          `json:"series_ticker"`
    SeriesTitle         string          `json:"series_title"`
    EventTicker         string          `json:"event_ticker"`
    EventSubtitle       string          `json:"event_subtitle"`
    EventTitle          string          `json:"event_title"`
    Category            string          `json:"category"`
    TotalSeriesVolume   int64           `json:"total_series_volume"`
    TotalVolume         int64           `json:"total_volume"`
    TotalMarketCount    int             `json:"total_market_count"`
    ActiveMarketCount   int             `json:"active_market_count"`
    SearchScore         int             `json:"search_score"`
    IsTrending          bool            `json:"is_trending"`
    IsNew               bool            `json:"is_new"`
    IsClosing           bool            `json:"is_closing"`
    IsPriceDelta        bool            `json:"is_price_delta"`
    FeeType             string          `json:"fee_type"`
    FeeMultiplier       float64         `json:"fee_multiplier"`
    Tags                []string        `json:"tags"`
    TopicKeywords       []string        `json:"topic_keywords"`
    MilestoneID         string          `json:"milestone_id"`
    ProductMetadata     ProductMetadata `json:"product_metadata"`
    Markets             []Market        `json:"markets"`
}

// Market is a single binary outcome within a Series.
// Prices are in cents (1–99). Multiply by 0.01 to get dollars.
type Market struct {
    Ticker               string            `json:"ticker"`
    YesSubtitle          string            `json:"yes_subtitle"`
    NoSubtitle           string            `json:"no_subtitle"`
    YesBid               int               `json:"yes_bid"`               // cents
    YesAsk               int               `json:"yes_ask"`               // cents
    LastPrice            int               `json:"last_price"`            // cents
    YesBidDollars        string            `json:"yes_bid_dollars"`
    YesAskDollars        string            `json:"yes_ask_dollars"`
    LastPriceDollars     string            `json:"last_price_dollars"`
    PriceDelta           int               `json:"price_delta"`           // cents
    PreviousPrice        int               `json:"previous_price"`        // cents
    PreviousPriceDollars string            `json:"previous_price_dollars"`
    Volume               int64             `json:"volume"`
    Score                int               `json:"score"`
    CloseTS              time.Time         `json:"close_ts"`
    ExpectedExpirationTS time.Time         `json:"expected_expiration_ts"`
    OpenTS               time.Time         `json:"open_ts"`
    Result               string            `json:"result"`
    CustomStrike         map[string]string `json:"custom_strike"`
    RulebookVariables    map[string]string `json:"rulebook_variables"`
    ImageURLDarkMode     string            `json:"image_url_dark_mode"`
    ImageURLLightMode    string            `json:"image_url_light_mode"`
    BackgroundColorDark  string            `json:"background_color_dark_mode"`
    BackgroundColorLight string            `json:"background_color_light_mode"`
    ImageScale           int               `json:"image_scale"`
}

// ProductMetadata holds editorial/categorisation data for a Series.
type ProductMetadata struct {
    Categories          []string            `json:"categories"`
    Subcategories       map[string][]string `json:"subcategories"`
    Scope               *string             `json:"scope"`
    PromotedMilestoneID string              `json:"promoted_milestone_id"`
}

// ExchangeStatus is returned by /v1/exchange/status.
type ExchangeStatus struct {
    ExchangeActive         bool `json:"exchange_active"`
    TradingActive          bool `json:"trading_active"`
    ImmediateFundingActive bool `json:"immediate_funding_active"`
}
```

---

### Client

```go
package kalshi

import (
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "net/url"
    "time"
)

const baseURL = "https://api.elections.kalshi.com"

type Client struct {
    http    *http.Client
    baseURL string
}

func NewClient() *Client {
    return &Client{
        http:    &http.Client{Timeout: 10 * time.Second},
        baseURL: baseURL,
    }
}

// SearchParams holds optional parameters for SearchSeries.
type SearchParams struct {
    PageSize int    // default: 5
    Page     int    // default: 1
    OrderBy  string // querymatch|volume|liquidity|trending|closing|newest
    Category string // optional category filter
    Status   string // optional status filter
}

// SearchSeries calls GET /v1/search/series and returns ranked series results.
func (c *Client) SearchSeries(ctx context.Context, query string, p SearchParams) (*SearchResponse, error) {
    if query == "" {
        return nil, fmt.Errorf("kalshi: query must not be empty")
    }

    q := url.Values{}
    q.Set("query", query)

    if p.PageSize > 0 {
        q.Set("page_size", fmt.Sprintf("%d", p.PageSize))
    }
    if p.Page > 1 {
        q.Set("page", fmt.Sprintf("%d", p.Page))
    }
    if p.OrderBy != "" {
        q.Set("order_by", p.OrderBy)
    }
    if p.Category != "" {
        q.Set("category", p.Category)
    }
    if p.Status != "" {
        q.Set("status", p.Status)
    }

    endpoint := fmt.Sprintf("%s/v1/search/series?%s", c.baseURL, q.Encode())

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
    if err != nil {
        return nil, fmt.Errorf("kalshi: build request: %w", err)
    }
    req.Header.Set("Accept", "application/json")

    resp, err := c.http.Do(req)
    if err != nil {
        return nil, fmt.Errorf("kalshi: do request: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        var errBody map[string]any
        _ = json.NewDecoder(resp.Body).Decode(&errBody)
        return nil, fmt.Errorf("kalshi: unexpected status %d: %v", resp.StatusCode, errBody)
    }

    var result SearchResponse
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, fmt.Errorf("kalshi: decode response: %w", err)
    }
    return &result, nil
}

// GetSeries fetches full series metadata for one or more tickers.
func (c *Client) GetSeries(ctx context.Context, tickers ...string) ([]SeriesDetail, error) {
    q := url.Values{}
    for i, t := range tickers {
        if i == 0 {
            q.Set("series_tickers", t)
        } else {
            q["series_tickers"][0] += "," + t
        }
    }

    endpoint := fmt.Sprintf("%s/v1/series/?%s", c.baseURL, q.Encode())

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
    if err != nil {
        return nil, fmt.Errorf("kalshi: build request: %w", err)
    }
    req.Header.Set("Accept", "application/json")

    resp, err := c.http.Do(req)
    if err != nil {
        return nil, fmt.Errorf("kalshi: do request: %w", err)
    }
    defer resp.Body.Close()

    var result struct {
        Series []SeriesDetail `json:"series"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, fmt.Errorf("kalshi: decode response: %w", err)
    }
    return result.Series, nil
}

// ExchangeStatus fetches live exchange status (use cached=true for the edge-cached version).
func (c *Client) ExchangeStatus(ctx context.Context, cached bool) (*ExchangeStatus, error) {
    path := "/v1/exchange/status"
    if cached {
        path = "/v1/cached/exchange/status"
    }

    req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
    if err != nil {
        return nil, err
    }
    req.Header.Set("Accept", "application/json")

    resp, err := c.http.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    var status ExchangeStatus
    return &status, json.NewDecoder(resp.Body).Decode(&status)
}
```

---

### HTTP Handler (proxy pattern — mirrors the Node.js implementation)

```go
package handler

import (
    "encoding/json"
    "net/http"
    "strconv"

    "yourmodule/kalshi"
)

type SearchHandler struct {
    client *kalshi.Client
}

func NewSearchHandler() *SearchHandler {
    return &SearchHandler{client: kalshi.NewClient()}
}

// ServeHTTP handles GET /api/search?query=...&page_size=5&order_by=querymatch
func (h *SearchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    query := r.URL.Query().Get("query")
    if query == "" {
        http.Error(w, `{"error":"missing query parameter"}`, http.StatusBadRequest)
        return
    }

    pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
    if pageSize == 0 {
        pageSize = 5
    }
    page, _ := strconv.Atoi(r.URL.Query().Get("page"))

    params := kalshi.SearchParams{
        PageSize: pageSize,
        Page:     page,
        OrderBy:  r.URL.Query().Get("order_by"),
        Category: r.URL.Query().Get("category"),
        Status:   r.URL.Query().Get("status"),
    }

    result, err := h.client.SearchSeries(r.Context(), query, params)
    if err != nil {
        w.WriteHeader(http.StatusBadGateway)
        json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(result)
}
```

---

### Router wiring (net/http or chi)

```go
// net/http
mux := http.NewServeMux()
mux.Handle("/api/search", handler.NewSearchHandler())
mux.Handle("/", http.FileServer(http.Dir("./public")))

// chi
r := chi.NewRouter()
r.Get("/api/search", handler.NewSearchHandler().ServeHTTP)
r.Handle("/*", http.FileServer(http.Dir("./public")))
```

---

## Key Differences from the Public v2 API

| Behaviour | v1 (`/v1/search/series`) | v2 (`/trade-api/v2/events`) |
|---|---|---|
| Text search | ✅ `?query=` works — strictly validated | ❌ `?search=` silently ignored |
| `order_by` | Strict enum — 400 on invalid value | Silently accepts anything, does nothing |
| Results shape | Series + nested markets inline | Events only; markets require a second call |
| Prices included | ✅ YES/NO bid/ask per market | ❌ Not in the events response |
| Pagination | `page` + `page_size` **and** cursor | Cursor only |
| Auth required | ❌ None for all read endpoints | ❌ None for read endpoints |
| Infrastructure | CloudFront → AWS | CloudFront → AWS |

---

## How the Current Node.js Proxy Works

The Node.js app (`server.js`) is a thin Express proxy:

1. **Serves** `public/index.html` as a static file.
2. **Exposes** `GET /api/search` which accepts `query`, `page_size`, and `order_by` as query params.
3. **Forwards** the request to `https://api.elections.kalshi.com/v1/search/series` with the same params.
4. **Returns** the raw Kalshi JSON response directly to the browser.

The proxy exists solely to avoid CORS restrictions when the frontend is served from `localhost` (or any non-Kalshi origin). The upstream Kalshi API requires no authentication headers.

The Go implementation should replicate this exact proxy pattern — one HTTP handler that translates incoming params, calls the upstream URL, and streams the JSON back.

---

## Testing

Verify the endpoint is working from the command line (no auth needed):

```bash
# Search
curl "https://api.elections.kalshi.com/v1/search/series?query=bitcoin&page_size=3&order_by=querymatch"

# Series detail
curl "https://api.elections.kalshi.com/v1/series/?series_tickers=KXBTC,KXOSCARDIR"

# Exchange status
curl "https://api.elections.kalshi.com/v1/cached/exchange/status"
```
