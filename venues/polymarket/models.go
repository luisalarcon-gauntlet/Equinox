package polymarket

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ─── Search API types (GET /public-search) ───────────────────────────────────
//
// Polymarket exposes two distinct API surfaces:
//
//   - Gamma API (https://gamma-api.polymarket.com): Market discovery.
//     No authentication required. Used by Equinox for topic search and price
//     discovery. Endpoints /public-search and /markets return descriptive
//     metadata, event groupings, and implied prices.
//
//   - CLOB API (https://clob.polymarket.com): Order execution.
//     Requires on-chain wallet signing (EIP-712). Provides access to the live
//     order book. NOT used by Equinox — this is a prototype, not a live trader.

// SearchResponse is the top-level JSON envelope returned by the Gamma
// /public-search endpoint. Unlike GET /markets (which returns a bare JSON
// array), the search endpoint wraps results inside this object so that markets
// can be grouped under their parent Event.
type SearchResponse struct {
	Events []Event `json:"events"`
}

// Event is a top-level Polymarket event — a named container that groups one
// or more related Market objects under a shared topic (e.g. "2026 US Senate
// Majority"). A single event may contain many binary markets, one per outcome.
type Event struct {
	Ticker      string   `json:"ticker"`
	Title       string   `json:"title"`
	Slug        string   `json:"slug"`
	Description string   `json:"description"`
	Active      bool     `json:"active"`
	Closed      bool     `json:"closed"`
	Archived    bool     `json:"archived"`
	Markets     []Market `json:"markets"`
}

// Market is an individual binary prediction market within a search Event.
//
// OutcomePrices is returned by the Gamma API as a JSON-encoded string array,
// e.g. `["0.65","0.35"]`. Index 0 is the Yes price; index 1 is the No price.
// Call parsePrices to populate YesPrice and NoPrice as usable float64 values.
type Market struct {
	ID            string `json:"id"`
	Question      string `json:"question"`
	OutcomePrices string `json:"outcomePrices"` // JSON string array, e.g. `["0.65","0.35"]`
	Active        bool   `json:"active"`
	Closed        bool   `json:"closed"`
	Archived      bool   `json:"archived"`
	EndDate       string `json:"endDate"` // RFC3339 or YYYY-MM-DD

	// AcceptingOrders signals whether the CLOB is currently accepting new orders.
	AcceptingOrders bool `json:"acceptingOrders"`

	// Volume / liquidity from the API response.
	Volume24hr   float64 `json:"volume24hr"`
	LiquidityNum float64 `json:"liquidityNum"`

	// Parsed price fields — absent from the JSON response.
	// Populated by parsePrices() after the raw API response is decoded.
	YesPrice float64 `json:"-"`
	NoPrice  float64 `json:"-"`
}

// parsePrices decodes OutcomePrices (a JSON-encoded string array) into
// YesPrice and NoPrice as float64 values. For standard binary markets index 0
// is Yes and index 1 is No. Missing or unparseable prices default to 0.
// The error is informational — the caller decides whether to skip the market.
func (m *Market) parsePrices() error {
	if m.OutcomePrices == "" {
		return nil
	}
	var raw []string
	if err := json.Unmarshal([]byte(m.OutcomePrices), &raw); err != nil {
		return fmt.Errorf("unmarshal outcomePrices: %w", err)
	}
	if len(raw) > 0 {
		if v, err := strconv.ParseFloat(strings.TrimSpace(raw[0]), 64); err == nil {
			m.YesPrice = v
		}
	}
	if len(raw) > 1 {
		if v, err := strconv.ParseFloat(strings.TrimSpace(raw[1]), 64); err == nil {
			m.NoPrice = v
		}
	}
	return nil
}

// ─── /markets endpoint type ───────────────────────────────────────────────────

// PolymarketMarket mirrors the market object returned by the Polymarket
// Gamma API GET /markets endpoint. All JSON tags match the snake_case and
// camelCase keys observed in the live API response.
//
// Pricing note: `outcomePrices` is a JSON-encoded string that contains an
// array of price strings, e.g. `["0.65", "0.35"]`. Index 0 is the Yes price,
// index 1 is the No price. `bestBid` and `bestAsk` represent the live
// order-book top-of-book and may be 0 when the CLOB is empty or the market
// is settled.
//
// Liquidity note: the `liquidity` field is a numeric string; `liquidityNum`
// is the float64 equivalent and is preferred. Similarly `volume` is a string
// and `volumeNum` is the float64.
type PolymarketMarket struct {
	ID          string `json:"id"`
	Question    string `json:"question"`
	ConditionID string `json:"conditionId"`
	Slug        string `json:"slug"`

	// Pricing — outcomePrices is a JSON-encoded string, e.g. '["0.65","0.35"]'.
	// bestBid and bestAsk are top-of-book prices from the CLOB (0.0–1.0).
	OutcomePrices string  `json:"outcomePrices"`
	BestBid       float64 `json:"bestBid"`
	BestAsk       float64 `json:"bestAsk"`
	Spread        float64 `json:"spread"`
	LastTradePrice float64 `json:"lastTradePrice"`

	// Timing — resolution order: endDate → endDateIso → closedTime → expiryDate → resolveTime.
	// Fields that are empty or unparseable are skipped; if none yield a valid
	// timestamp, ResolvesAt is set to the zero value and a warning is emitted.
	EndDate     string `json:"endDate"`     // RFC3339, e.g. "2026-11-04T00:00:00Z"
	EndDateIso  string `json:"endDateIso"`  // ISO date, e.g. "2026-11-04" (fallback)
	ClosedTime  string `json:"closedTime"`  // RFC3339 timestamp when the market was closed
	ExpiryDate  string `json:"expiryDate"`  // ISO date, e.g. "2026-11-04" (tertiary fallback)
	ResolveTime string `json:"resolveTime"` // RFC3339 resolution timestamp (final fallback)

	// Liquidity / volume
	LiquidityNum float64 `json:"liquidityNum"` // float64 equivalent of liquidity string
	VolumeNum    float64 `json:"volumeNum"`    // total volume in dollars
	Volume24hr   float64 `json:"volume24hr"`   // 24-hour volume in dollars

	// Status
	Active   bool `json:"active"`
	Closed   bool `json:"closed"`
	Archived bool `json:"archived"`

	// Metadata
	Category string   `json:"category"`
	Tags     []string `json:"tags"` // optional tag list; category falls back to tags[0]

	// Outcomes — typically ["Yes", "No"] for binary markets.
	Outcomes string `json:"outcomes"` // JSON-encoded string, e.g. '["Yes","No"]'

	// Market type indicator.
	MarketType string `json:"marketType"` // "normal", etc.

	// Price change metrics (informational, not used for routing).
	OneDayPriceChange  float64 `json:"oneDayPriceChange"`
	OneWeekPriceChange float64 `json:"oneWeekPriceChange"`
}
