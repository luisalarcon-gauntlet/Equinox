package kalshi

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	equinoxerrors "github.com/equinox/errors"
	"github.com/equinox/models"
)

// marketNamespace is the UUID v5 namespace used to generate deterministic
// internal IDs from Kalshi tickers. The same ticker always produces the same
// UUID, which simplifies caching and deduplication.
var marketNamespace = uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

// AdaptKalshiMarket transforms one v1 search result plus one of its nested
// market entries into the canonical models.Market struct used downstream.
func AdaptKalshiMarket(series KalshiSeriesResult, raw KalshiMarket) (models.Market, error) {
	if raw.Ticker == "" {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "kalshi",
			Message: "market ticker is empty",
		}
	}

	yesBid, err := parseSearchPrice(raw.YesBidDollars, raw.YesBid)
	if err != nil {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "kalshi",
			Message: fmt.Sprintf("invalid yes_bid price for %s", raw.Ticker),
			Err:     err,
		}
	}

	yesAsk, err := parseSearchPrice(raw.YesAskDollars, raw.YesAsk)
	if err != nil {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "kalshi",
			Message: fmt.Sprintf("invalid yes_ask price for %s", raw.Ticker),
			Err:     err,
		}
	}

	resolvesAt, err := parseCloseTime(raw)
	if err != nil {
		return models.Market{}, err
	}

	title := buildCanonicalTitle(series, raw)
	mid := (yesBid + yesAsk) / 2
	liquidity := deriveLiquidity(series, raw)
	category := strings.ToLower(series.Category)
	if category == "" {
		category = categorizeSearchResult(series)
	}

	return models.Market{
		ID:             generateID(raw.Ticker),
		VenueID:        raw.Ticker,
		Venue:          "kalshi",
		Title:          normalizeTitle(title),
		YesBid:         yesBid,
		YesAsk:         yesAsk,
		YesMid:         mid,
		NoPrice:        1.0 - mid,
		Spread:         yesAsk - yesBid,
		Liquidity:      liquidity,
		Volume24h:      float64(raw.Volume),
		ResolvesAt:     resolvesAt,
		ResolutionDate: resolvesAt.UTC().Format("2006-01-02"),
		FetchedAt:      time.Now(),
		Underlying:     extractKalshiUnderlying(series, raw),
		StrikePrice:    parseKalshiStrikePrice(raw),
		Category:       category,
		Status:         mapKalshiStatus(raw.Status),
		RawData: RawSearchMarket{
			Series: series,
			Market: raw,
		},
	}, nil
}

func parseSearchPrice(dollarValue string, centValue int) (float64, error) {
	if dollarValue != "" {
		return parseDollarPrice(dollarValue)
	}
	if centValue < 0 || centValue > 100 {
		return 0, fmt.Errorf("cent price %d outside valid range", centValue)
	}
	return float64(centValue) / 100.0, nil
}

func parseCloseTime(raw KalshiMarket) (time.Time, error) {
	closeTS := raw.CloseTS
	if closeTS == "" {
		closeTS = raw.ExpectedExpirationTS
	}
	if closeTS == "" {
		return time.Time{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "kalshi",
			Message: "market close timestamp is empty",
		}
	}

	resolvesAt, err := time.Parse(time.RFC3339, closeTS)
	if err != nil {
		return time.Time{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "kalshi",
			Message: fmt.Sprintf("failed to parse close_ts: %s", closeTS),
			Err:     err,
		}
	}
	return resolvesAt, nil
}

func buildCanonicalTitle(series KalshiSeriesResult, raw KalshiMarket) string {
	switch {
	case series.EventTitle != "" && raw.YesSubtitle != "":
		return fmt.Sprintf("%s (%s)", series.EventTitle, raw.YesSubtitle)
	case series.EventTitle != "":
		return series.EventTitle
	case series.SeriesTitle != "" && raw.YesSubtitle != "":
		return fmt.Sprintf("%s (%s)", series.SeriesTitle, raw.YesSubtitle)
	case series.SeriesTitle != "":
		return series.SeriesTitle
	case raw.YesSubtitle != "":
		return raw.YesSubtitle
	default:
		return raw.Ticker
	}
}

func deriveLiquidity(series KalshiSeriesResult, raw KalshiMarket) float64 {
	if raw.Volume > 0 {
		return float64(raw.Volume)
	}
	if series.TotalVolume > 0 {
		return float64(series.TotalVolume)
	}
	return 0
}

// parseDollarPrice converts a fixed-point dollar string (e.g. "0.5600")
// to a float64 in [0, 1]. An empty string is treated as 0.0.
func parseDollarPrice(s string) (float64, error) {
	if s == "" {
		return 0.0, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	if v < 0 || v > 1 {
		return 0, fmt.Errorf("price %f is outside valid range [0, 1]", v)
	}
	return v, nil
}

// mapKalshiStatus converts a Kalshi market lifecycle status into the canonical
// statuses used by the routing engine. v1 search results are open markets, so
// an empty status defaults to "open".
func mapKalshiStatus(status string) string {
	switch strings.ToLower(status) {
	case "", "active", "open":
		return "open"
	case "initialized", "inactive", "pending":
		return "pending"
	default:
		return "closed"
	}
}

// extractKalshiUnderlying returns a normalized asset symbol (e.g. "SOL", "BTC")
// from the series metadata for same-underlying equivalence pre-filtering.
func extractKalshiUnderlying(series KalshiSeriesResult, raw KalshiMarket) string {
	parts := []string{
		series.SeriesTicker,
		series.EventTicker,
		series.SeriesTitle,
		series.EventTitle,
		strings.Join(series.Tags, " "),
		strings.Join(series.TopicKeywords, " "),
		raw.YesSubtitle,
	}
	upper := strings.ToUpper(strings.Join(parts, " "))
	switch {
	case strings.Contains(upper, "KXSOLE") || strings.Contains(upper, "SOLANA") || (strings.Contains(upper, "SOL") && strings.Contains(upper, "PRICE")):
		return "SOL"
	case strings.Contains(upper, "KXBTCD") || strings.Contains(upper, "BITCOIN") || strings.Contains(upper, " BTC"):
		return "BTC"
	case strings.Contains(upper, "ETHEREUM") || strings.Contains(upper, " ETH"):
		return "ETH"
	default:
		return ""
	}
}

func parseKalshiStrikePrice(raw KalshiMarket) float64 {
	candidates := []string{raw.YesSubtitle}
	for _, v := range raw.CustomStrike {
		candidates = append(candidates, v)
	}
	text := strings.Join(candidates, " ")
	if text == "" {
		return 0
	}

	orAbove := regexp.MustCompile(`(?i)(\d[\d,]*(?:\.\d+)?)\s*(?:or\s+)?(?:above|higher)`)
	if m := orAbove.FindStringSubmatch(text); len(m) >= 2 {
		if v, err := parseNumericToken(m[1]); err == nil {
			return v
		}
	}

	rangeRe := regexp.MustCompile(`(\d[\d,]*(?:\.\d+)?)\s*to\s*\d`)
	if m := rangeRe.FindStringSubmatch(text); len(m) >= 2 {
		if v, err := parseNumericToken(m[1]); err == nil {
			return v
		}
	}

	numRe := regexp.MustCompile(`(\d[\d,]*(?:\.\d+)?)`)
	if m := numRe.FindStringSubmatch(text); len(m) >= 2 {
		if v, err := parseNumericToken(m[1]); err == nil {
			return v
		}
	}

	return 0
}

func parseNumericToken(s string) (float64, error) {
	return strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
}

func categorizeSearchResult(series KalshiSeriesResult) string {
	upper := strings.ToUpper(strings.Join([]string{
		series.SeriesTicker,
		series.EventTicker,
		series.SeriesTitle,
		series.EventTitle,
		strings.Join(series.Tags, " "),
		strings.Join(series.TopicKeywords, " "),
	}, " "))
	switch {
	case strings.Contains(upper, "BTC") ||
		strings.Contains(upper, "ETH") ||
		strings.Contains(upper, "CRYPTO") ||
		strings.Contains(upper, "SOL"):
		return "crypto"
	case strings.Contains(upper, "PRES") ||
		strings.Contains(upper, "SENATE") ||
		strings.Contains(upper, "HOUSE") ||
		strings.Contains(upper, "ELEC") ||
		strings.Contains(upper, "GOV"):
		return "politics"
	case strings.Contains(upper, "FED") ||
		strings.Contains(upper, "GDP") ||
		strings.Contains(upper, "CPI") ||
		strings.Contains(upper, "ECON"):
		return "economics"
	case strings.Contains(upper, "NBA") ||
		strings.Contains(upper, "NFL") ||
		strings.Contains(upper, "MLB") ||
		strings.Contains(upper, "SPORT"):
		return "sports"
	default:
		return "other"
	}
}

// v2EventToSeries builds a minimal KalshiSeriesResult from a v2 event for use with AdaptKalshiMarket.
func v2EventToSeries(ev V2Event) KalshiSeriesResult {
	return KalshiSeriesResult{
		EventTicker:   ev.EventTicker,
		SeriesTicker:  ev.SeriesTicker,
		EventTitle:    ev.Title,
		EventSubtitle: ev.SubTitle,
		Category:      ev.Category,
	}
}

// v2MarketToKalshiMarket copies fields from v2 market into KalshiMarket so AdaptKalshiMarket can be used.
func v2MarketToKalshiMarket(m V2Market) KalshiMarket {
	return KalshiMarket{
		Ticker:               m.Ticker,
		YesSubtitle:          m.YesSubtitle,
		NoSubtitle:           m.NoSubtitle,
		YesBid:               m.YesBid,
		YesAsk:               m.YesAsk,
		LastPrice:            m.LastPrice,
		YesBidDollars:        m.YesBidDollars,
		YesAskDollars:        m.YesAskDollars,
		LastPriceDollars:     m.LastPriceDollars,
		Volume:               m.Volume,
		CloseTS:              m.CloseTS,
		ExpectedExpirationTS: m.ExpectedExpirationTS,
		OpenTS:               m.OpenTS,
		Status:               m.Status,
		Result:               m.Result,
		CustomStrike:         m.CustomStrike,
		RulebookVariables:    m.RulebookVariables,
	}
}

// AdaptV2EventMarkets converts a v2 event and its nested markets into canonical models.Market slice.
// Uses the same filtering as the v1 path: open status, no result, non-degenerate prices.
func AdaptV2EventMarkets(ev V2Event) ([]models.Market, error) {
	series := v2EventToSeries(ev)
	var out []models.Market
	for i := range ev.Markets {
		leg := v2MarketToKalshiMarket(ev.Markets[i])
		if leg.Status != "" && mapKalshiStatus(leg.Status) != "open" {
			continue
		}
		if leg.Result != "" {
			continue
		}
		m, err := AdaptKalshiMarket(series, leg)
		if err != nil {
			continue
		}
		if m.YesMid <= degeneratePriceThreshold || m.YesMid >= (1.0-degeneratePriceThreshold) {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// generateID creates a deterministic UUID v5 from a Kalshi ticker so the same
// market always gets the same internal ID across fetches.
func generateID(ticker string) string {
	return uuid.NewSHA1(marketNamespace, []byte("kalshi:"+ticker)).String()
}

var punctuationRe = regexp.MustCompile(`[^\w\s]`)

// normalizeTitle lowercases, strips punctuation, and collapses whitespace so
// the equivalence detector can compare titles from different venues fairly.
func normalizeTitle(title string) string {
	title = strings.ToLower(title)
	title = punctuationRe.ReplaceAllString(title, "")
	title = strings.Join(strings.Fields(title), " ")
	return strings.TrimSpace(title)
}
