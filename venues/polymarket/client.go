package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/equinox/config"
	equinoxerrors "github.com/equinox/errors"
	"github.com/equinox/logger"
	"github.com/equinox/models"
)

// PolymarketClient fetches market data from the Polymarket Gamma API and
// adapts raw responses into canonical models.Market structs.
//
// Unlike Kalshi, the Polymarket Gamma API is fully public and requires no
// authentication. The response is a JSON array (not a wrapped object), which
// the client handles by decoding directly into []PolymarketMarket.
type PolymarketClient struct {
	httpClient *http.Client
	baseURL    string
	log        *logger.Logger
}

// NewPolymarketClient constructs a PolymarketClient from the provided config.
func NewPolymarketClient(cfg *config.Config, log *logger.Logger) *PolymarketClient {
	return &PolymarketClient{
		httpClient: &http.Client{Timeout: cfg.HTTPTimeout},
		baseURL:    cfg.PolymarketBaseURL,
		log:        log,
	}
}

// GetVenueName satisfies the VenueConnector interface.
func (c *PolymarketClient) GetVenueName() string { return "polymarket" }

// polyDegeneratePriceThreshold mirrors the Kalshi threshold: a market whose
// implied Yes mid price is within this distance of 0 or 1 is treated as
// effectively decided and excluded from results.
const polyDegeneratePriceThreshold = 0.03

// FetchMarkets queries the Polymarket /public-search endpoint (via
// SearchActiveMarkets) and adapts every surviving market into a canonical
// models.Market. The /public-search endpoint returns current, properly
// grouped events — unlike /markets, which returns stale historical data.
//
// All filtering (active, closed, stale year, end-date freshness, degenerate
// price) is handled inside SearchActiveMarkets before markets reach this
// method. Markets that pass all guards but fail adaptation are logged and
// skipped — partial results are always better than aborting the entire fetch.
func (c *PolymarketClient) FetchMarkets(ctx context.Context, query string) ([]models.Market, error) {
	c.log.Info("connector", "polymarket", fmt.Sprintf("fetching markets for query: %q", query))

	events, err := c.SearchActiveMarkets(ctx, query)
	if err != nil {
		return nil, err
	}

	markets := make([]models.Market, 0)
	for _, event := range events {
		for _, sm := range event.Markets {
			m, err := AdaptSearchMarket(event, sm)
			if err != nil {
				c.log.Warn("connector", "polymarket",
					fmt.Sprintf("skipping search market %q: %v", sm.ID, err))
				continue
			}
			markets = append(markets, m)
		}
	}

	c.log.Info("connector", "polymarket", fmt.Sprintf(
		"fetched %d markets from %d events for query %q",
		len(markets), len(events), query))
	return markets, nil
}

// ─── Search discovery service (GET /public-search) ───────────────────────────

// safetyYear is the minimum acceptable market year. Any market whose EndDate
// or question text references a year strictly before this value is treated as
// a stale historical result and excluded from SearchActiveMarkets output.
const safetyYear = 2025

// staleYearRe matches any 4-digit year in the range 2000–2024 (inclusive).
// A match in a market title or EndDate is strong evidence of a historical
// record that pre-dates the safety cutoff and should be filtered out.
var staleYearRe = regexp.MustCompile(`\b(20(?:0\d|1\d|2[0-4]))\b`)

// SearchActiveMarkets queries the Gamma /public-search endpoint and returns
// only events that are open and contain at least one active, non-stale market.
//
// # Gamma API vs CLOB API
//
// This method uses the Gamma API exclusively:
//
//   - Gamma API (/public-search): Returns Event objects that group related
//     markets under a shared topic. Ideal for cross-venue equivalence detection.
//     No authentication; read-only metadata and implied prices.
//
//   - CLOB API (clob.polymarket.com): Provides live order-book data and is the
//     gateway for on-chain order submission. Requires wallet signing. NOT used
//     by Equinox — this is a discovery prototype, not a trading system.
//
// # Filters applied client-side
//
//  1. Event-level: event must be Active, not Closed, and not Archived.
//  2. Market-level: market.Active == true, not Closed, not Archived.
//  3. Safety Year: events or markets whose title/EndDate reference any year
//     prior to safetyYear (2025) are discarded to prevent 2020/2021 election
//     artefacts from polluting results.
//  4. End-date freshness: markets whose EndDate is parseable and in the past
//     are excluded.
//  5. Degenerate price: markets whose YesPrice is within 0.03 of 0 or 1 are
//     excluded — they are effectively decided.
//
// OutcomePrices on every surviving market are parsed into float64 YesPrice and
// NoPrice fields before the slice is returned.
// queryFallbacks returns the ordered list of query strings to try when the
// Polymarket /public-search returns no active events for the original query.
//
// Polymarket's search engine does not stem or lemmatize words, so a query like
// "oscar best actor 2026" will not match events whose slugs contain "oscars"
// (plural). The fallback chain progressively broadens the query:
//
//  1. First word pluralized: "oscar best actor 2026" → "oscars best actor 2026"
//     Covers the most common mismatch — singular noun vs plural event title.
//  2. First word only: "oscar" — cast the widest possible net when the
//     pluralized full query still yields nothing.
//
// Each fallback is tried in order; we return as soon as one yields active events.
func queryFallbacks(original string) []string {
	words := strings.Fields(original)
	if len(words) == 0 {
		return nil
	}
	first := words[0]
	var fallbacks []string

	// Pluralized first word + rest of query (e.g. "oscar …" → "oscars …").
	if !strings.HasSuffix(first, "s") {
		pluralized := first + "s"
		rest := strings.Join(words[1:], " ")
		if rest != "" {
			fallbacks = append(fallbacks, pluralized+" "+rest)
		} else {
			fallbacks = append(fallbacks, pluralized)
		}
	}

	// Just the first word — maximally broad fallback.
	if len(words) > 1 {
		fallbacks = append(fallbacks, first)
	}

	return fallbacks
}

func (c *PolymarketClient) SearchActiveMarkets(ctx context.Context, query string) ([]Event, error) {
	c.log.Info("connector", "polymarket",
		fmt.Sprintf("searching active markets for query: %q", query))

	filtered, err := c.searchWithFallbacks(ctx, query)
	if err != nil {
		return nil, err
	}

	c.log.Info("connector", "polymarket", fmt.Sprintf(
		"SearchActiveMarkets: %d active events for query %q", len(filtered), query))

	return filtered, nil
}

// searchWithFallbacks tries the original query, then each fallback from
// queryFallbacks, returning as soon as any attempt yields at least one active event.
func (c *PolymarketClient) searchWithFallbacks(ctx context.Context, query string) ([]Event, error) {
	queries := append([]string{query}, queryFallbacks(query)...)

	for i, q := range queries {
		if i > 0 {
			c.log.Info("connector", "polymarket",
				fmt.Sprintf("0 active events for %q — retrying with fallback query %q", queries[i-1], q))
		}

		events, err := c.fetchSearchPage(ctx, q)
		if err != nil {
			return nil, err
		}

		rawCount := len(events)
		filtered := filterAndEnrichEvents(events, c.log)

		c.log.Debug("connector", "polymarket", fmt.Sprintf(
			"query %q: raw=%d active=%d (discarded=%d)",
			q, rawCount, len(filtered), rawCount-len(filtered),
		))

		if len(filtered) > 0 {
			return filtered, nil
		}
	}

	return nil, nil
}

// fetchSearchPage performs a single GET /public-search request and returns the
// raw event slice from the response. All HTTP and JSON errors are wrapped.
func (c *PolymarketClient) fetchSearchPage(ctx context.Context, query string) ([]Event, error) {
	fullURL := c.buildSearchURL(query)
	c.log.Debug("connector", "polymarket",
		fmt.Sprintf("search request URL: %s", fullURL))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "polymarket",
			Message: "failed to build search request",
			Err:     err,
		}
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "polymarket",
			Message: "search HTTP request failed",
			Err:     err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("status %d", resp.StatusCode)
		c.log.Error("connector", "polymarket", "non-200 search response", statusErr)
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "polymarket",
			Message: fmt.Sprintf("search API returned status %d", resp.StatusCode),
			Err:     statusErr,
		}
	}

	var searchResp SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "polymarket",
			Message: "failed to decode search response",
			Err:     err,
		}
	}

	return searchResp.Events, nil
}

// buildSearchURL constructs the full /public-search URL with the query string
// encoded as the `q` parameter.
func (c *PolymarketClient) buildSearchURL(query string) string {
	params := url.Values{}
	params.Set("q", query)
	return fmt.Sprintf("%s/public-search?%s", c.baseURL, params.Encode())
}

// filterAndEnrichEvents applies all client-side filters (closed/archived/inactive
// event, inactive/closed market, stale year, end-date freshness, degenerate
// price) and calls parsePrices on every surviving market. Events left with zero
// active markets after filtering are themselves dropped.
func filterAndEnrichEvents(events []Event, log *logger.Logger) []Event {
	result := make([]Event, 0, len(events))
	for _, ev := range events {
		if !ev.Active {
			log.Debug("connector", "polymarket",
				fmt.Sprintf("skip inactive event %q", ev.Slug))
			continue
		}

		if ev.Closed || ev.Archived {
			log.Debug("connector", "polymarket",
				fmt.Sprintf("skip closed/archived event %q (closed=%t archived=%t)",
					ev.Slug, ev.Closed, ev.Archived))
			continue
		}

		if containsStaleYear(ev.Title) {
			log.Debug("connector", "polymarket",
				fmt.Sprintf("skip stale-year event title %q", ev.Title))
			continue
		}

		activeMarkets := filterAndEnrichMarkets(ev.Markets, log)
		if len(activeMarkets) == 0 {
			log.Debug("connector", "polymarket",
				fmt.Sprintf("skip event %q: no active markets remain after filtering", ev.Slug))
			continue
		}

		ev.Markets = activeMarkets
		result = append(result, ev)
	}
	return result
}

// filterAndEnrichMarkets applies market-level filters and parses OutcomePrices
// into float64 fields on every market that passes. Filters applied:
//
//  1. Active flag — must be true.
//  2. Closed / Archived — must both be false.
//  3. Safety year — title or EndDate must not reference a year < 2025.
//  4. End-date freshness — if EndDate parses, it must not be in the past.
//  5. Degenerate price — YesPrice must not be within 0.03 of 0 or 1.
func filterAndEnrichMarkets(markets []Market, log *logger.Logger) []Market {
	result := make([]Market, 0, len(markets))
	for _, m := range markets {
		if !m.Active {
			continue
		}

		if m.Closed || m.Archived {
			log.Debug("connector", "polymarket", fmt.Sprintf(
				"skip closed/archived market %q (closed=%t archived=%t)",
				m.ID, m.Closed, m.Archived))
			continue
		}

		if isMarketStale(m) {
			log.Debug("connector", "polymarket",
				fmt.Sprintf("skip stale-year market %q (endDate=%s)", m.ID, m.EndDate))
			continue
		}

		if isSearchMarketEndDatePast(m) {
			log.Debug("connector", "polymarket",
				fmt.Sprintf("skip past-endDate market %q (endDate=%s)", m.ID, m.EndDate))
			continue
		}

		if err := m.parsePrices(); err != nil {
			log.Debug("connector", "polymarket",
				fmt.Sprintf("market %q: price parse warning: %v", m.ID, err))
		}

		if m.YesPrice > 0 && (m.YesPrice <= polyDegeneratePriceThreshold || m.YesPrice >= (1.0-polyDegeneratePriceThreshold)) {
			log.Debug("connector", "polymarket", fmt.Sprintf(
				"skip degenerate-price market %q (yesPrice=%.4f)", m.ID, m.YesPrice))
			continue
		}

		result = append(result, m)
	}
	return result
}

// containsStaleYear reports whether s contains a 4-digit year in [2000, 2024].
// A match indicates that the string references a period before the safetyYear
// cutoff, making the associated market a stale historical result.
func containsStaleYear(s string) bool {
	return staleYearRe.MatchString(s)
}

// isMarketStale returns true when either the market's question text or its
// EndDate references a year strictly less than safetyYear (2025).
func isMarketStale(m Market) bool {
	if containsStaleYear(m.Question) {
		return true
	}
	if len(m.EndDate) >= 4 {
		year, err := strconv.Atoi(m.EndDate[:4])
		if err == nil && year < safetyYear {
			return true
		}
	}
	return false
}

// isSearchMarketEndDatePast returns true when the market's EndDate is
// parseable and has already passed. Markets with unparseable or empty
// EndDate fields pass through (cannot determine staleness).
func isSearchMarketEndDatePast(m Market) bool {
	if m.EndDate == "" {
		return false
	}
	if t, err := time.Parse(time.RFC3339, m.EndDate); err == nil {
		return t.Before(time.Now())
	}
	if t, err := time.Parse("2006-01-02", m.EndDate); err == nil {
		return t.UTC().Before(time.Now())
	}
	return false
}
