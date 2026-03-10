package kalshi

// KalshiMarket mirrors the market object returned by the Kalshi Trade API v2
// GET /markets endpoint. Fields are tagged to match the snake_case JSON keys.
//
// Pricing note: the `_dollars` fields are the authoritative price source —
// they are fixed-point decimal strings in [0, 1] representing the dollar
// value of a Yes contract (which pays $1 at settlement). The legacy integer
// `yes_bid`/`yes_ask` fields (in cents) are deprecated by Kalshi and ignored.
//
// Liquidity note: `liquidity` and `liquidity_dollars` are deprecated by Kalshi
// and always return 0. We use `open_interest` (contract count) as a liquidity
// proxy for routing comparisons.
type KalshiMarket struct {
	Ticker      string `json:"ticker"`       // e.g. "KXBTCD-25DEC31-B100000"
	EventTicker string `json:"event_ticker"` // e.g. "KXBTCD-25DEC31"

	// Titles — `title` is deprecated by Kalshi; prefer `yes_sub_title`.
	Title      string `json:"title"`
	YesSubTitle string `json:"yes_sub_title"` // shortened question for the Yes side
	NoSubTitle  string `json:"no_sub_title"`

	MarketType string `json:"market_type"` // "binary" or "scalar"

	// Status lifecycle: initialized → inactive → active → closed →
	// determined → disputed / amended → finalized
	Status string `json:"status"`

	// Prices as fixed-point dollar strings, e.g. "0.5600".
	// Range is [0, 1] (a Kalshi contract pays $1 on resolution).
	YesBidDollars string `json:"yes_bid_dollars"`
	YesAskDollars string `json:"yes_ask_dollars"`
	NoBidDollars  string `json:"no_bid_dollars"`
	NoAskDollars  string `json:"no_ask_dollars"`

	LastPriceDollars string `json:"last_price_dollars"`

	// Volume in contract units (integer counts).
	Volume    int `json:"volume"`
	Volume24h int `json:"volume_24h"`

	// OpenInterest is the number of outstanding contracts.
	// Used as a liquidity proxy since the `liquidity` field is deprecated.
	OpenInterest   int    `json:"open_interest"`
	OpenInterestFp string `json:"open_interest_fp"` // same value, fixed-point string

	// Timestamps in RFC3339 format.
	CloseTime   string `json:"close_time"`
	OpenTime    string `json:"open_time"`
	CreatedTime string `json:"created_time"`

	// SettlementTs is set after the market settles (nullable in the API).
	// A non-empty value is a definitive signal that the market is finished.
	SettlementTs string `json:"settlement_ts"`

	// Rules text (used for audit / display only, not for routing logic).
	RulesPrimary   string `json:"rules_primary"`
	RulesSecondary string `json:"rules_secondary"`

	// Result is set after determination: "yes", "no", "scalar", or "".
	Result string `json:"result"`
}

// KalshiMarketsResponse is the top-level envelope returned by GET /markets.
// Retained for reference; active fetching now uses KalshiEventsResponse.
type KalshiMarketsResponse struct {
	Markets []KalshiMarket `json:"markets"`
	Cursor  string         `json:"cursor"` // opaque pagination token
}

// KalshiEvent mirrors the event object returned by GET /events with
// with_nested_markets=true. An event groups one or more related market legs
// (e.g. different strike prices) under a single human-readable title.
// This is the authoritative source for the canonical question text — the
// per-market `title` field is deprecated by Kalshi and often cryptic.
type KalshiEvent struct {
	EventTicker  string         `json:"event_ticker"`  // e.g. "KXFEDRATE-25MAY"
	SeriesTicker string         `json:"series_ticker"` // parent series, e.g. "KXFEDRATE" (the "Drawer")
	Title        string         `json:"title"`         // full human-readable question
	Category     string         `json:"category"`      // e.g. "Economics", "Politics"
	Markets      []KalshiMarket `json:"markets"`       // nested market legs for this event
}

// KalshiEventsResponse is the top-level envelope returned by GET /events.
type KalshiEventsResponse struct {
	Events []KalshiEvent `json:"events"`
	Cursor string        `json:"cursor"` // opaque pagination token
}

// KalshiSeries mirrors the series object returned by GET /series.
// A series is the top-level grouping on Kalshi (the "Drawer") that contains
// one or more related Events. The Tags field is Kalshi's own discoverability
// metadata — it is the primary signal used by the series relevance scorer.
type KalshiSeries struct {
	Ticker    string   `json:"ticker"`    // e.g. "KXBTCD"
	Title     string   `json:"title"`     // e.g. "Bitcoin Daily Close"
	Category  string   `json:"category"`  // e.g. "crypto", "economics"
	Tags      []string `json:"tags"`      // Kalshi-curated subject tags, e.g. ["bitcoin","btc"]
	Frequency string   `json:"frequency"` // e.g. "daily", "weekly", "one-off"
}

// KalshiSeriesResponse is the top-level envelope returned by GET /series.
type KalshiSeriesResponse struct {
	Series []KalshiSeries `json:"series"`
}
