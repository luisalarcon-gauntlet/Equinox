package kalshi

// SearchParams controls the GET /v1/search/series request.
// Query is required by the upstream API; the remaining fields are optional.
type SearchParams struct {
	Query    string
	PageSize int
	Page     int
	Cursor   string
	OrderBy  string
	Category string
	Status   string
}

// KalshiMarket mirrors one nested market entry returned by GET /v1/search/series.
// Prices are exposed both as cents and as fixed-point dollar strings.
type KalshiMarket struct {
	Ticker string `json:"ticker"`

	YesSubtitle string `json:"yes_subtitle"`
	NoSubtitle  string `json:"no_subtitle"`

	YesBid    int `json:"yes_bid"`
	YesAsk    int `json:"yes_ask"`
	LastPrice int `json:"last_price"`

	YesBidDollars    string `json:"yes_bid_dollars"`
	YesAskDollars    string `json:"yes_ask_dollars"`
	LastPriceDollars string `json:"last_price_dollars"`

	PriceDelta    int `json:"price_delta"`
	PreviousPrice int `json:"previous_price"`

	Volume int64 `json:"volume"`
	Score  int   `json:"score"`

	CloseTS              string `json:"close_ts"`
	ExpectedExpirationTS string `json:"expected_expiration_ts"`
	OpenTS               string `json:"open_ts"`

	// Some v1 responses include status; when omitted we infer "open" from the endpoint.
	Status string `json:"status"`
	Result string `json:"result"`

	CustomStrike         map[string]string `json:"custom_strike"`
	RulebookVariables    map[string]string `json:"rulebook_variables"`
	ImageURLDarkMode     string            `json:"image_url_dark_mode"`
	ImageURLLightMode    string            `json:"image_url_light_mode"`
	BackgroundColorDark  string            `json:"background_color_dark_mode"`
	BackgroundColorLight string            `json:"background_color_light_mode"`
	ImageScale           int               `json:"image_scale"`
	PreviousPriceDollars string            `json:"previous_price_dollars"`
}

// ProductMetadata carries editorial classification from Kalshi.
type ProductMetadata struct {
	Categories          []string            `json:"categories"`
	Subcategories       map[string][]string `json:"subcategories"`
	Scope               *string             `json:"scope"`
	PromotedMilestoneID string              `json:"promoted_milestone_id"`
}

// KalshiSeriesResult is one ranked series entry returned by GET /v1/search/series.
// Each result includes event context and nested active markets.
type KalshiSeriesResult struct {
	SeriesTicker  string `json:"series_ticker"`
	SeriesTitle   string `json:"series_title"`
	EventTicker   string `json:"event_ticker"`
	EventTitle    string `json:"event_title"`
	EventSubtitle string `json:"event_subtitle"`
	Category      string `json:"category"`

	TotalSeriesVolume int64 `json:"total_series_volume"`
	TotalVolume       int64 `json:"total_volume"`

	TotalMarketCount  int `json:"total_market_count"`
	ActiveMarketCount int `json:"active_market_count"`
	SearchScore       int `json:"search_score"`

	IsTrending   bool `json:"is_trending"`
	IsNew        bool `json:"is_new"`
	IsClosing    bool `json:"is_closing"`
	IsPriceDelta bool `json:"is_price_delta"`

	FeeType       string  `json:"fee_type"`
	FeeMultiplier float64 `json:"fee_multiplier"`

	Tags            []string        `json:"tags"`
	TopicKeywords   []string        `json:"topic_keywords"`
	MilestoneID     string          `json:"milestone_id"`
	ProductMetadata ProductMetadata `json:"product_metadata"`

	Markets []KalshiMarket `json:"markets"`
}

// SearchResponse is the top-level GET /v1/search/series envelope.
type SearchResponse struct {
	TotalResultsCount int                  `json:"total_results_count"`
	NextCursor        string               `json:"next_cursor"`
	CurrentPage       []KalshiSeriesResult `json:"current_page"`
}

// SeriesDetail mirrors GET /v1/series/ detail responses.
type SeriesDetail struct {
	Ticker        string  `json:"ticker"`
	Title         string  `json:"title"`
	Category      string  `json:"category"`
	Frequency     string  `json:"frequency"`
	FeeType       string  `json:"fee_type"`
	FeeMultiplier float64 `json:"fee_multiplier"`
	SeriesVolume  int64   `json:"series_volume"`

	ContractTicker string `json:"contract_ticker"`
	ContractURL    string `json:"contract_url"`

	Keywords []string `json:"keywords"`
	Tags     []string `json:"tags"`

	ProductMetadata SeriesDetailProductMetadata `json:"product_metadata"`
}

type SeriesDetailProductMetadata struct {
	About             string              `json:"about"`
	Scope             string              `json:"scope"`
	Subcategories     map[string][]string `json:"subcategories"`
	SuggesterNickname string              `json:"suggester_nickname"`
}

// SeriesResponse is the top-level GET /v1/series/ envelope.
type SeriesResponse struct {
	Series []SeriesDetail `json:"series"`
}

// RawSearchMarket preserves the original v1 series result and nested market
// payload for audit/debug use inside the canonical model's RawData field.
type RawSearchMarket struct {
	Series KalshiSeriesResult `json:"series"`
	Market KalshiMarket       `json:"market"`
}

// V2EventResponse is the response from GET trade-api/v2/events/{ticker}?with_nested_markets=true.
// Markets are nested inside the event when with_nested_markets=true.
type V2EventResponse struct {
	Event V2Event `json:"event"`
}

// V2Event is the event object in the v2 API. Field names follow Kalshi's API.
type V2Event struct {
	EventTicker   string       `json:"event_ticker"`
	SeriesTicker  string       `json:"series_ticker"`
	Title         string       `json:"title"`
	SubTitle      string       `json:"sub_title"`
	Category      string       `json:"category"`
	Markets       []V2Market   `json:"markets"`
}

// V2Market is one market in the v2 event's nested markets array.
// The v2 API uses different timestamp field names than v1 (close_time vs
// close_ts, expected_expiration_time vs expected_expiration_ts).
// v2MarketToKalshiMarket maps v2 names onto the v1 KalshiMarket fields.
type V2Market struct {
	Ticker string `json:"ticker"`

	YesSubtitle string `json:"yes_subtitle"`
	NoSubtitle  string `json:"no_subtitle"`

	YesBid    int `json:"yes_bid"`
	YesAsk    int `json:"yes_ask"`
	LastPrice int `json:"last_price"`

	YesBidDollars    string `json:"yes_bid_dollars"`
	YesAskDollars    string `json:"yes_ask_dollars"`
	LastPriceDollars string `json:"last_price_dollars"`

	Volume int64 `json:"volume"`

	// v2 API timestamp field names (different from v1's close_ts / expected_expiration_ts).
	CloseTime              string `json:"close_time"`
	ExpectedExpirationTime string `json:"expected_expiration_time"`
	OpenTime               string `json:"open_time"`

	Status string `json:"status"`
	Result string `json:"result"`

	CustomStrike      map[string]string `json:"custom_strike"`
	RulebookVariables map[string]string `json:"rulebook_variables"`
}
