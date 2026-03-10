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

// AdaptKalshiMarket transforms a KalshiEvent + one of its nested KalshiMarket
// legs into the canonical models.Market struct used by every downstream
// component.
//
// Title strategy:
//   - Always starts with event.Title (the clean human-readable question).
//   - If the market leg carries a YesSubTitle (the strike-level qualifier,
//     e.g. ">5.25%"), it is appended in parentheses so the heuristic matcher
//     receives a full sentence: "Federal Reserve rate decision (>5.25%)".
//   - Falls back to the raw ticker only when both title sources are empty.
//
// Category strategy:
//   - Uses event.Category (lowercased) when the API returns one.
//   - Falls back to ticker-prefix heuristics via categorizeByEventTicker.
//
// Error checkpoints:
//   - empty ticker → "market ticker is empty"
//   - unparseable close_time → "failed to parse close_time"
//   - prices outside [0, 1] → "invalid price range"
func AdaptKalshiMarket(event KalshiEvent, raw KalshiMarket) (models.Market, error) {
	if raw.Ticker == "" {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "kalshi",
			Message: "market ticker is empty",
		}
	}

	yesBid, err := parseDollarPrice(raw.YesBidDollars)
	if err != nil {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "kalshi",
			Message: fmt.Sprintf("invalid yes_bid_dollars: %s", raw.YesBidDollars),
			Err:     err,
		}
	}

	yesAsk, err := parseDollarPrice(raw.YesAskDollars)
	if err != nil {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "kalshi",
			Message: fmt.Sprintf("invalid yes_ask_dollars: %s", raw.YesAskDollars),
			Err:     err,
		}
	}

	resolvesAt, err := time.Parse(time.RFC3339, raw.CloseTime)
	if err != nil {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "kalshi",
			Message: fmt.Sprintf("failed to parse close_time: %s", raw.CloseTime),
			Err:     err,
		}
	}

	mid := (yesBid + yesAsk) / 2

	liquidity := 0.0
	if raw.OpenInterestFp != "" {
		if liq, parseErr := strconv.ParseFloat(raw.OpenInterestFp, 64); parseErr == nil {
			liquidity = liq
		}
	}

	// Build title: event title + optional strike subtitle in parens.
	title := event.Title
	if raw.YesSubTitle != "" {
		title = fmt.Sprintf("%s (%s)", event.Title, raw.YesSubTitle)
	}
	if title == "" {
		title = raw.Ticker
	}

	// Use the event's category when available; fall back to ticker heuristics.
	category := strings.ToLower(event.Category)
	if category == "" {
		category = categorizeByEventTicker(raw.EventTicker)
	}

	resolutionDate := resolvesAt.UTC().Format("2006-01-02")
	underlying := extractKalshiUnderlying(event, raw)
	strikePrice := parseKalshiStrikePrice(raw.YesSubTitle)

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
		Volume24h:      float64(raw.Volume24h),
		ResolvesAt:     resolvesAt,
		ResolutionDate: resolutionDate,
		FetchedAt:      time.Now(),
		Underlying:     underlying,
		StrikePrice:    strikePrice,
		Category:       category,
		Status:         mapKalshiStatus(raw.Status),
		RawData:        raw,
	}, nil
}

// parseDollarPrice converts a Kalshi fixed-point dollar string (e.g. "0.5600")
// to a float64 in [0, 1]. An empty string is treated as 0.0 (valid, no error).
// A value outside [0, 1] is an error.
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
// statuses used by the routing engine.
//
// Kalshi lifecycle: initialized → inactive → active → closed →
// determined → disputed / amended → finalized.
//
// Only "active" maps to "open" — it is the sole state where the market is
// accepting orders. "initialized" and "inactive" are pre-trading states that
// map to "pending".
func mapKalshiStatus(status string) string {
	switch status {
	case "active":
		return "open"
	case "initialized", "inactive":
		return "pending"
	default:
		return "closed"
	}
}

// extractKalshiUnderlying returns a normalized asset symbol (e.g. "SOL", "BTC")
// from the event ticker or title for same-underlying equivalence pre-filtering.
func extractKalshiUnderlying(event KalshiEvent, raw KalshiMarket) string {
	upper := strings.ToUpper(event.EventTicker + " " + event.SeriesTicker + " " + event.Title)
	switch {
	case strings.Contains(upper, "KXSOLE") || strings.Contains(upper, "SOLANA") || (strings.Contains(upper, "SOL ") && strings.Contains(upper, "PRICE")):
		return "SOL"
	case strings.Contains(upper, "KXBTCD") || strings.Contains(upper, "BITCOIN") || strings.Contains(upper, "BTC"):
		return "BTC"
	case strings.Contains(upper, "ETH"):
		return "ETH"
	default:
		return ""
	}
}

// parseKalshiStrikePrice extracts a numeric threshold from YesSubTitle for
// price markets, e.g. "89 or above" -> 89, "81 to 819999" -> 81. Returns 0
// when no parseable threshold is found.
func parseKalshiStrikePrice(yesSubTitle string) float64 {
	if yesSubTitle == "" {
		return 0
	}
	// Match "N or above" or "N or higher", or first number in "N to M" range.
	orAbove := regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(?:or\s+)?(?:above|higher)`)
	if m := orAbove.FindStringSubmatch(yesSubTitle); len(m) >= 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v
		}
	}
	rangeRe := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*to\s*\d+`)
	if m := rangeRe.FindStringSubmatch(yesSubTitle); len(m) >= 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v
		}
	}
	// Single number as fallback.
	numRe := regexp.MustCompile(`(\d+(?:\.\d+)?)`)
	if m := numRe.FindStringSubmatch(yesSubTitle); len(m) >= 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v
		}
	}
	return 0
}

// categorizeByEventTicker derives a category string from the Kalshi event
// ticker prefix. Kalshi does not expose a category field directly; the ticker
// prefix is the best public signal available.
func categorizeByEventTicker(eventTicker string) string {
	upper := strings.ToUpper(eventTicker)
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
