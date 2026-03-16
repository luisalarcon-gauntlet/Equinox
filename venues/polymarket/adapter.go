package polymarket

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	equinoxerrors "github.com/equinox/errors"
	"github.com/equinox/logger"
	"github.com/equinox/models"
)

// polymarketNamespace is the UUID v5 namespace for Polymarket market IDs.
var polymarketNamespace = uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")

// AdaptPolymarketMarket transforms a raw Polymarket Gamma API market into the
// canonical models.Market used by every downstream component.
//
// Price resolution order:
//  1. bestBid / bestAsk from the CLOB (non-zero → preferred, reflects live order book)
//  2. outcomePrices[0] fallback (last traded / implied price when book is empty)
//
// Date resolution order (first successfully parsed field wins):
//  1. endDate (RFC3339)
//  2. endDateIso (YYYY-MM-DD, UTC midnight)
//  3. closedTime (RFC3339)
//  4. expiryDate (YYYY-MM-DD, UTC midnight)
//  5. resolveTime (RFC3339)
//
// If no date field is parseable, ResolvesAt is set to time.Time{} (zero value)
// and a WARN log is emitted. The market is still returned — the equivalence
// detector is responsible for handling markets with unknown resolution dates.
//
// Error checkpoints:
//   - empty ID → "venue ID is empty"
//   - prices outside [0, 1] → "invalid price range"
func AdaptPolymarketMarket(raw PolymarketMarket) (models.Market, error) {
	if raw.ID == "" {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "polymarket",
			Message: "market ID is empty",
		}
	}

	yesBid, yesAsk, err := resolvePrices(raw)
	if err != nil {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "polymarket",
			Message: "invalid market prices",
			Err:     err,
		}
	}

	resolvesAt, dateFound := resolveDate(raw)
	if !dateFound {
		logger.Default().Warn(
			"normalizer",
			"polymarket",
			fmt.Sprintf("no parseable date for market %s; ResolvesAt set to zero", raw.ID),
		)
	}

	mid := (yesBid + yesAsk) / 2

	var resolutionDate string
	if dateFound {
		resolutionDate = resolvesAt.UTC().Format("2006-01-02")
	}
	underlying := extractPolymarketUnderlying(raw.Question, raw.Tags)
	strikePrice := parsePolymarketStrikePrice(raw.Question)

	return models.Market{
		ID:             generatePolyID(raw.ID),
		VenueID:        raw.ID,
		Venue:          "polymarket",
		Title:          normalizeTitle(raw.Question),
		YesBid:         yesBid,
		YesAsk:         yesAsk,
		YesMid:         mid,
		NoPrice:        1.0 - mid,
		Spread:         yesAsk - yesBid,
		Liquidity:      raw.LiquidityNum,
		Volume24h:      raw.Volume24hr,
		ResolvesAt:     resolvesAt,
		ResolutionDate: resolutionDate,
		FetchedAt:      time.Now(),
		Underlying:     underlying,
		StrikePrice:    strikePrice,
		Category:       resolveCategory(raw),
		Status:         mapPolymarketStatus(raw),
		RawData:        raw,
	}, nil
}

// resolvePrices determines YesBid and YesAsk from the raw market data.
// bestBid/bestAsk take priority when at least one is non-zero (live order book).
// When both are zero, outcomePrices[0] is used as the implied mid price and
// both bid and ask are set to it (spread = 0, indicating no live book).
func resolvePrices(raw PolymarketMarket) (yesBid, yesAsk float64, err error) {
	if raw.BestBid != 0 || raw.BestAsk != 0 {
		if raw.BestBid < 0 || raw.BestBid > 1 || raw.BestAsk < 0 || raw.BestAsk > 1 {
			return 0, 0, fmt.Errorf("bestBid=%f or bestAsk=%f outside [0,1]",
				raw.BestBid, raw.BestAsk)
		}
		return raw.BestBid, raw.BestAsk, nil
	}

	// Fall back to outcomePrices[0].
	if raw.OutcomePrices == "" {
		return 0, 0, nil
	}
	yesPrice, parseErr := parseOutcomePrices(raw.OutcomePrices)
	if parseErr != nil {
		// Non-fatal: default to zero rather than failing the whole market.
		return 0, 0, nil
	}
	return yesPrice, yesPrice, nil
}

// parseOutcomePrices decodes the JSON-encoded price array (e.g. `["0.65","0.35"]`)
// and returns the first element as a float64 (Yes price).
func parseOutcomePrices(raw string) (float64, error) {
	var prices []string
	if err := json.Unmarshal([]byte(raw), &prices); err != nil {
		return 0, fmt.Errorf("unmarshal outcomePrices: %w", err)
	}
	if len(prices) == 0 {
		return 0, fmt.Errorf("outcomePrices is empty array")
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(prices[0]), 64)
	if err != nil {
		return 0, fmt.Errorf("parse outcomePrices[0] %q: %w", prices[0], err)
	}
	return v, nil
}

// resolveDate walks through all known date fields in priority order, returning
// the first one that parses successfully together with true. If no field yields
// a valid timestamp, it returns (time.Time{}, false) — the caller is
// responsible for logging a warning and continuing with a zero ResolvesAt.
//
// Priority: endDate → endDateIso → closedTime → expiryDate → resolveTime.
// RFC3339 fields are parsed with time.RFC3339; ISO date fields ("2006-01-02")
// are interpreted as UTC midnight.
func resolveDate(raw PolymarketMarket) (time.Time, bool) {
	if raw.EndDate != "" {
		if t, err := time.Parse(time.RFC3339, raw.EndDate); err == nil {
			return t, true
		}
	}

	if raw.EndDateIso != "" {
		if t, err := time.Parse("2006-01-02", raw.EndDateIso); err == nil {
			return t.UTC(), true
		}
	}

	if raw.ClosedTime != "" {
		if t, err := time.Parse(time.RFC3339, raw.ClosedTime); err == nil {
			return t, true
		}
	}

	if raw.ExpiryDate != "" {
		if t, err := time.Parse("2006-01-02", raw.ExpiryDate); err == nil {
			return t.UTC(), true
		}
	}

	if raw.ResolveTime != "" {
		if t, err := time.Parse(time.RFC3339, raw.ResolveTime); err == nil {
			return t, true
		}
	}

	return time.Time{}, false
}

// extractPolymarketUnderlying returns a normalized asset symbol (e.g. "SOL", "BTC")
// from the question or tags for same-underlying equivalence pre-filtering.
func extractPolymarketUnderlying(question string, tags []string) string {
	lower := strings.ToLower(question)
	for _, t := range tags {
		lower += " " + strings.ToLower(t)
	}
	switch {
	case strings.Contains(lower, "solana") || (strings.Contains(lower, "sol ") && strings.Contains(lower, "price")):
		return "SOL"
	case strings.Contains(lower, "bitcoin") || strings.Contains(lower, " btc "):
		return "BTC"
	case strings.Contains(lower, "ethereum") || strings.Contains(lower, " eth "):
		return "ETH"
	default:
		return ""
	}
}

// parsePolymarketStrikePrice extracts a numeric threshold from the question
// for price markets, e.g. "above 90" -> 90, "below 80" -> 80. Returns 0 when
// no parseable threshold is found.
func parsePolymarketStrikePrice(question string) float64 {
	if question == "" {
		return 0
	}
	// "above 90", "above $90", "be above 90"
	aboveRe := regexp.MustCompile(`(?i)(?:above|over)\s*\$?(\d+(?:\.\d+)?)`)
	if m := aboveRe.FindStringSubmatch(question); len(m) >= 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v
		}
	}
	belowRe := regexp.MustCompile(`(?i)(?:below|under)\s*\$?(\d+(?:\.\d+)?)`)
	if m := belowRe.FindStringSubmatch(question); len(m) >= 2 {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v
		}
	}
	return 0
}

// resolveCategory returns the market category. The `category` field is used
// directly when populated; otherwise the first tag is used; and "other" is
// the final fallback.
func resolveCategory(raw PolymarketMarket) string {
	if raw.Category != "" {
		return raw.Category
	}
	if len(raw.Tags) > 0 && raw.Tags[0] != "" {
		return raw.Tags[0]
	}
	return "other"
}

// mapPolymarketStatus converts the Polymarket active/closed boolean pair into
// the canonical "open" / "closed" status string.
func mapPolymarketStatus(raw PolymarketMarket) string {
	if raw.Closed {
		return "closed"
	}
	return "open"
}

var polyPunctuationRe = regexp.MustCompile(`[^\w\s]`)

// normalizeTitle lowercases, strips punctuation, and collapses whitespace.
func normalizeTitle(title string) string {
	title = strings.ToLower(title)
	title = polyPunctuationRe.ReplaceAllString(title, "")
	title = strings.Join(strings.Fields(title), " ")
	return strings.TrimSpace(title)
}

// generatePolyID creates a deterministic UUID v5 from a Polymarket market ID.
func generatePolyID(id string) string {
	return uuid.NewSHA1(polymarketNamespace, []byte("polymarket:"+id)).String()
}

// AdaptCLOBMarket converts a CLOBMarket (GET /markets/{conditionId}) into the
// canonical models.Market. Prices come from Tokens[0].Price (the first outcome,
// treated as "Yes"). When the market has fewer than 2 tokens or the condition ID
// is empty, an error is returned.
//
// Spread is set to 0 because the CLOB /markets endpoint exposes a mid price per
// token, not an explicit top-of-book bid/ask. Both YesBid and YesAsk are set to
// the same price so downstream spread-based filters don't reject the market.
func AdaptCLOBMarket(raw CLOBMarket) (models.Market, error) {
	if raw.ConditionID == "" {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "polymarket",
			Message: "CLOB market condition_id is empty",
		}
	}
	if len(raw.Tokens) == 0 {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "polymarket",
			Message: "CLOB market has no tokens",
		}
	}

	// Use first token as the "Yes" outcome price.
	yesPrice := raw.Tokens[0].Price
	if yesPrice < 0 || yesPrice > 1 {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "polymarket",
			Message: fmt.Sprintf("CLOB token price %.4f outside [0,1]", yesPrice),
		}
	}

	// Parse resolution date from end_date_iso (RFC3339 or YYYY-MM-DD).
	var resolvesAt time.Time
	var resolutionDate string
	if raw.EndDateIso != "" {
		if t, err := time.Parse(time.RFC3339, raw.EndDateIso); err == nil {
			resolvesAt = t
			resolutionDate = t.UTC().Format("2006-01-02")
		} else if t, err := time.Parse("2006-01-02", raw.EndDateIso); err == nil {
			resolvesAt = t.UTC()
			resolutionDate = t.Format("2006-01-02")
		}
	}

	status := "open"
	if raw.Closed {
		status = "closed"
	}

	underlying := extractPolymarketUnderlying(raw.Question, raw.Tags)
	strikePrice := parsePolymarketStrikePrice(raw.Question)

	category := "other"
	if len(raw.Tags) > 0 && raw.Tags[0] != "" {
		category = raw.Tags[0]
	}

	return models.Market{
		ID:             generatePolyID(raw.ConditionID),
		VenueID:        raw.ConditionID,
		Venue:          "polymarket",
		Title:          normalizeTitle(raw.Question),
		YesBid:         yesPrice,
		YesAsk:         yesPrice,
		YesMid:         yesPrice,
		NoPrice:        1.0 - yesPrice,
		Spread:         0,
		ResolvesAt:     resolvesAt,
		ResolutionDate: resolutionDate,
		FetchedAt:      time.Now(),
		Underlying:     underlying,
		StrikePrice:    strikePrice,
		Category:       category,
		Status:         status,
		RawData:        raw,
	}, nil
}

// AdaptSearchMarket transforms a /public-search Market (and its parent Event)
// into the canonical models.Market. This is the adapter used by FetchMarkets
// after SearchActiveMarkets returns filtered, price-enriched results.
//
// Price source: YesPrice / NoPrice fields populated by parsePrices() during
// filterAndEnrichMarkets. Search results lack bid/ask spread, so both YesBid
// and YesAsk are set to YesPrice (spread = 0).
func AdaptSearchMarket(event Event, sm Market) (models.Market, error) {
	if sm.ID == "" {
		return models.Market{}, &equinoxerrors.EquinoxError{
			Layer:   "normalizer",
			Venue:   "polymarket",
			Message: "search market ID is empty",
		}
	}

	mid := sm.YesPrice

	var resolvesAt time.Time
	var resolutionDate string
	if sm.EndDate != "" {
		if t, err := time.Parse(time.RFC3339, sm.EndDate); err == nil {
			resolvesAt = t
			resolutionDate = t.UTC().Format("2006-01-02")
		} else if t, err := time.Parse("2006-01-02", sm.EndDate); err == nil {
			resolvesAt = t.UTC()
			resolutionDate = t.Format("2006-01-02")
		}
	}

	// Use the market's own question; fall back to the event title if empty.
	title := sm.Question
	if title == "" {
		title = event.Title
	}

	status := "open"
	if sm.Closed {
		status = "closed"
	}

	underlying := extractPolymarketUnderlying(sm.Question, nil)
	strikePrice := parsePolymarketStrikePrice(sm.Question)

	return models.Market{
		ID:             generatePolyID(sm.ID),
		VenueID:        sm.ID,
		Venue:          "polymarket",
		Title:          normalizeTitle(title),
		YesBid:         mid,
		YesAsk:         mid,
		YesMid:         mid,
		NoPrice:        1.0 - mid,
		Spread:         0, // search results lack bid/ask spread
		Liquidity:      sm.LiquidityNum,
		Volume24h:      sm.Volume24hr,
		ResolvesAt:     resolvesAt,
		ResolutionDate: resolutionDate,
		FetchedAt:      time.Now(),
		Underlying:     underlying,
		StrikePrice:    strikePrice,
		Category:       "other",
		Status:         status,
		RawData:        sm,
	}, nil
}
