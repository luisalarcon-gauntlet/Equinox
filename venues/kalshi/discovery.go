package kalshi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	equinoxerrors "github.com/equinox/errors"
)

const (
	// discoveryPageLimit is the maximum number of events to request per page.
	// 200 is the Kalshi API ceiling and minimises round-trips when walking the
	// full open-event catalogue.
	discoveryPageLimit = 200

	// discoveryRateDelay is the pause injected between successive paginated
	// requests. Kalshi's basic tier supports ~20 req/s; 50 ms keeps us safely
	// within that budget while leaving headroom for other concurrent callers.
	discoveryRateDelay = 50 * time.Millisecond
)

// SearchAllOpenEvents pages through every open event on the Kalshi exchange
// and returns those that match query via token-based case-insensitive matching.
//
// Each whitespace-delimited token in the query must appear somewhere in the
// combined text of the event title and market subtitles (AND semantics).
// A single-word query behaves as a simple substring check. An empty query
// matches every event.
//
// # How cursor-based pagination covers every Drawer (Series) and Folder (Event)
//
// Kalshi structures its exchange as a two-level tree:
//
//	Series "Drawer"  → e.g. "Fed Rate Decisions"   (SeriesTicker: KXFEDRATE)
//	  Event "Folder" → e.g. "May 2026 meeting"      (EventTicker:  KXFEDRATE-26MAY)
//	    Market "Leg" → e.g. "> 4.75%"               (Ticker:       KXFEDRATE-26MAY-T475)
//
// A single GET /events?status=open&limit=200 returns up to 200 Events sorted
// by the exchange's internal ordering — this cut spans ALL Series in one stream.
// Each response carries an opaque `cursor` token encoding "resume after this
// item". Passing that token as &cursor=… on the next request advances the
// stream by exactly one page. When the API returns an empty cursor there are
// no more Events to serve: every open Folder in every Drawer has been visited,
// regardless of how many Series exist or how many Events each one contains.
func (c *KalshiClient) SearchAllOpenEvents(ctx context.Context, query string) ([]KalshiEvent, error) {
	c.log.Info("connector", "kalshi", fmt.Sprintf("starting full discovery for query %q", query))

	var (
		allMatches []KalshiEvent
		cursor     string
		page       int
	)

	for {
		pageURL := c.buildDiscoveryURL(cursor)
		c.log.Debug("connector", "kalshi", fmt.Sprintf("discovery page %d: %s", page+1, pageURL))

		raw, err := c.fetchPage(ctx, pageURL)
		if err != nil {
			return nil, err
		}

		for _, event := range raw.Events {
			c.log.Debug("connector", "kalshi", fmt.Sprintf(
				"raw event: %s | %q", event.EventTicker, event.Title,
			))
			if eventMatchesDiscoveryQuery(event, query) {
				allMatches = append(allMatches, event)
			}
		}

		page++
		c.log.Info("connector", "kalshi", fmt.Sprintf(
			"discovery page %d: %d events scanned, %d matches so far",
			page, len(raw.Events), len(allMatches),
		))

		// An empty cursor signals the final page — every open Folder has been visited.
		if raw.Cursor == "" {
			break
		}
		cursor = raw.Cursor

		// Throttle before fetching the next page to respect Kalshi's rate limit.
		// The select also listens for context cancellation so callers can abort
		// mid-pagination without waiting out the full 50 ms delay.
		select {
		case <-ctx.Done():
			return nil, &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: "context cancelled during paginated discovery",
				Err:     ctx.Err(),
			}
		case <-time.After(discoveryRateDelay):
		}
	}

	c.log.Info("connector", "kalshi", fmt.Sprintf(
		"discovery complete: %d pages, %d matching events for query %q",
		page, len(allMatches), query,
	))
	return allMatches, nil
}

// buildDiscoveryURL constructs the GET /events URL for full-catalogue discovery.
//
// Unlike buildURL (used by FetchMarkets), no server-side search= filter is
// applied here. We intentionally request every open event and apply client-side
// two-layer matching so that subtitle-only matches are never silently dropped
// by the server before we get a chance to inspect them.
//
// min_close_ts is set to the current Unix timestamp so the API excludes events
// whose every market leg has already closed. This is a Kalshi-native filter
// that reduces payload size before any client-side filtering runs.
func (c *KalshiClient) buildDiscoveryURL(cursor string) string {
	params := url.Values{}
	params.Set("status", "open")
	params.Set("with_nested_markets", "true")
	params.Set("limit", "200")
	params.Set("min_close_ts", strconv.FormatInt(time.Now().Unix(), 10))
	if cursor != "" {
		params.Set("cursor", cursor)
	}
	return fmt.Sprintf("%s/events?%s", c.baseURL, params.Encode())
}

// fetchPage issues a single signed GET to fullURL, decodes the JSON body into
// a KalshiEventsResponse, and returns the envelope. It delegates transport and
// signing to doSignedGet so all auth and error-handling logic lives in one place.
func (c *KalshiClient) fetchPage(ctx context.Context, fullURL string) (*KalshiEventsResponse, error) {
	resp, err := c.doSignedGet(ctx, fullURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw KalshiEventsResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshi",
			Message: "failed to decode JSON response",
			Err:     err,
		}
	}
	return &raw, nil
}

// ── Series-targeted search ────────────────────────────────────────────────────

// WarmSeriesCache fetches the complete Kalshi series list from GET /series and
// populates the in-memory cache. It should be called once at startup so that
// subsequent FetchMarkets calls use series-targeted fetching rather than the
// full catalogue scan.
//
// A failure here is non-fatal: FetchMarkets falls back to SearchAllOpenEvents
// when the cache is cold, so the system remains fully operational.
func (c *KalshiClient) WarmSeriesCache(ctx context.Context) error {
	c.log.Info("connector", "kalshi", "warming series cache via GET /series")

	series, err := c.fetchAllSeries(ctx)
	if err != nil {
		return err
	}

	c.seriesCache.populate(series)
	c.log.Info("connector", "kalshi",
		fmt.Sprintf("series cache warmed: %d series indexed", len(series)))
	for _, s := range series {
		c.log.Debug("connector", "kalshi", fmt.Sprintf(
			"  [series] ticker=%-20s category=%-14s tags=%v title=%q",
			s.Ticker, s.Category, s.Tags, s.Title,
		))
	}
	return nil
}

// fetchAllSeries calls GET /series and returns all Kalshi series entries.
func (c *KalshiClient) fetchAllSeries(ctx context.Context) ([]KalshiSeries, error) {
	fullURL := fmt.Sprintf("%s/series", c.baseURL)
	c.log.Debug("connector", "kalshi", fmt.Sprintf("fetching series list: %s", fullURL))

	resp, err := c.doSignedGet(ctx, fullURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw KalshiSeriesResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "connector",
			Venue:   "kalshi",
			Message: "failed to decode series response",
			Err:     err,
		}
	}
	return raw.Series, nil
}

// maxParallelSeriesFetch caps the number of concurrent GET /events requests
// made by searchEventsBySeries. This keeps us comfortably within Kalshi's
// basic-tier rate limit (~20 req/s) even when many series match a query.
const maxParallelSeriesFetch = 5

// searchEventsBySeries consults the series cache to identify which series are
// relevant to query, then fetches their events concurrently (up to
// maxParallelSeriesFetch in parallel).
//
// No per-event token filter is applied here. The series scoring layer (with
// its absolute and relative score floors) has already ensured every series in
// relevant is a strong semantic match for the query. Re-filtering individual
// events by query tokens is counterproductive: Kalshi event titles use
// idiosyncratic phrasing ("Will X win Best Actor?") that rarely contains every
// user-supplied word verbatim (e.g. "oscars", "winner"). The equivalence
// detector downstream handles precision; this layer's job is recall within
// the already-targeted series.
//
// If the cache is cold or no series score above the relevance floor, the
// function returns an empty slice — FetchMarkets falls back to
// SearchAllOpenEvents in that case.
//
// Individual series fetch failures are logged and skipped; partial results are
// always returned rather than aborting the entire search.
func (c *KalshiClient) searchEventsBySeries(ctx context.Context, query string) ([]KalshiEvent, error) {
	relevant := c.seriesCache.RelevantSeries(query)
	if len(relevant) == 0 {
		c.log.Info("connector", "kalshi",
			fmt.Sprintf("series cache cold or no matches for query %q; caller should fall back", query))
		return nil, nil
	}

	c.log.Info("connector", "kalshi",
		fmt.Sprintf("series-targeted search: %d series matched for query %q", len(relevant), query))

	var (
		mu      sync.Mutex
		results []KalshiEvent
		wg      sync.WaitGroup
	)

	sem := make(chan struct{}, maxParallelSeriesFetch)

	for _, s := range relevant {
		wg.Add(1)
		sem <- struct{}{}
		go func(ticker string) {
			defer wg.Done()
			defer func() { <-sem }()

			events, err := c.fetchSeriesEvents(ctx, ticker)
			if err != nil {
				c.log.Warn("connector", "kalshi",
					fmt.Sprintf("series %q: event fetch failed: %v", ticker, err))
				return
			}

			mu.Lock()
			results = append(results, events...)
			mu.Unlock()
		}(s.Ticker)
	}

	wg.Wait()

	c.log.Info("connector", "kalshi",
		fmt.Sprintf("series-targeted search complete: %d events for query %q", len(results), query))
	return results, nil
}

// fetchSeriesEvents pages through all open events for a single series ticker
// using GET /events?series_ticker=X. Pagination follows the same cursor
// pattern as the full-catalogue scan but is scoped to one series at a time.
func (c *KalshiClient) fetchSeriesEvents(ctx context.Context, seriesTicker string) ([]KalshiEvent, error) {
	var (
		allEvents []KalshiEvent
		cursor    string
	)

	for {
		raw, err := c.fetchPage(ctx, c.buildSeriesEventsURL(seriesTicker, cursor))
		if err != nil {
			return nil, err
		}
		allEvents = append(allEvents, raw.Events...)

		if raw.Cursor == "" {
			break
		}
		cursor = raw.Cursor

		// Honour context cancellation between pages.
		select {
		case <-ctx.Done():
			return nil, &equinoxerrors.EquinoxError{
				Layer:   "connector",
				Venue:   "kalshi",
				Message: fmt.Sprintf("context cancelled paging series %q", seriesTicker),
				Err:     ctx.Err(),
			}
		default:
		}
	}

	return allEvents, nil
}

// buildSeriesEventsURL constructs a GET /events URL scoped to a single series.
// It mirrors buildDiscoveryURL but adds the series_ticker filter so only events
// belonging to that series are returned.
func (c *KalshiClient) buildSeriesEventsURL(seriesTicker, cursor string) string {
	params := url.Values{}
	params.Set("status", "open")
	params.Set("series_ticker", seriesTicker)
	params.Set("with_nested_markets", "true")
	params.Set("limit", "200")
	params.Set("min_close_ts", strconv.FormatInt(time.Now().Unix(), 10))
	if cursor != "" {
		params.Set("cursor", cursor)
	}
	return fmt.Sprintf("%s/events?%s", c.baseURL, params.Encode())
}

// discoveryStopwords are common words in natural-language queries that often
// do not appear verbatim in Kalshi event titles or market subtitles. Requiring
// them would cause false misses (e.g. "Price of Solana end of march" would
// fail if the event title is "Solana (SOL) Price - March 31, 2025" and "of"
// is absent). Only meaningful tokens are required for AND semantics.
var discoveryStopwords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "in": true, "on": true,
	"at": true, "for": true, "to": true, "by": true, "with": true, "from": true,
	"and": true, "or": true, "but": true, "will": true, "be": true, "is": true,
	"are": true, "was": true, "were": true, "been": true, "being": true,
	"have": true, "has": true, "had": true, "do": true, "does": true, "did": true,
	"what": true, "when": true, "where": true, "which": true, "who": true,
	"how": true, "if": true, "then": true, "than": true, "that": true,
	"end": true, // "end of march" -> march is the signal; many titles use "March 31" not "end"
}

// discoveryTokenSynonyms maps a query token to alternative strings that satisfy
// the match when present in event text. Kalshi often uses "SOL" in titles while
// users type "Solana"; they use "Mar" for March. So we accept either form.
// "winner" maps to "win"/"wins" because Kalshi event titles phrase markets as
// "Will X win Best Actor?" — the root verb, not the noun form the user typed.
// "oscars" maps to "oscar" (singular) because Kalshi event titles may use the
// singular form ("Oscar Best Actor") while users naturally write the plural.
// Note: "oscar" in a query already substring-matches "oscars" in event text
// (containment is asymmetric), so only the plural→singular direction is needed.
var discoveryTokenSynonyms = map[string][]string{
	"solana":  {"sol"},
	"march":   {"mar"},
	"bitcoin": {"btc"},
	"winner":  {"win", "wins"},
	"winners": {"win", "wins"},
	"oscars":  {"oscar"},
}

// punctuation is the set of characters trimmed from the leading and trailing
// edges of each query token. Whitespace-splitting alone leaves attached
// punctuation as part of the token (e.g. "2026:" from "Oscars 2026: Best
// Actor"), which can never match event titles that contain only "2026".
const punctuation = `.,;:!?'"()[]{}—–-`

// yearTokenRe matches a 4-digit year in the range 2000–2099. Year tokens are
// treated as optional in eventMatchesDiscoveryQuery: Kalshi award/entertainment
// market titles ("Will X win Best Actor at the Oscars?") typically omit the
// year even though the underlying event is year-specific. Requiring year tokens
// in AND semantics would reject all such events when the user's query includes
// "2026". Year tokens still contribute to series scoring (series_cache.go) so
// they retain semantic weight at the discovery-targeting level.
var yearTokenRe = regexp.MustCompile(`^20\d{2}$`)

// discoveryQueryTokens returns the lowercased, whitespace-split tokens from
// query with leading/trailing punctuation stripped and stopwords removed.
// Used so natural-language queries like "Oscars 2026: Best Actor Winner"
// produce clean tokens ["oscars", "2026", "best", "actor", "winner"] rather
// than ["oscars", "2026:", "best", "actor", "winner"], which would cause the
// AND-semantics event filter to reject every event that lacks the literal
// string "2026:".
func discoveryQueryTokens(query string) []string {
	raw := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	var out []string
	for _, tok := range raw {
		tok = strings.Trim(tok, punctuation)
		if tok == "" || discoveryStopwords[tok] {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// eventMatchesDiscoveryQuery checks whether every meaningful (non-stopword)
// token in the query appears somewhere in the combined text of the event title
// and market subtitles (AND semantics, case-insensitive). Stopwords are
// ignored so natural-language queries like "Price of Solana end of march"
// match events titled "Solana (SOL) Price - End of March 2025". An empty query
// or a query that yields no tokens after stopword removal matches every event.
//
// The text pool merges the event title with all market subtitles so that a
// token satisfied by a subtitle and another by the title still produces a
// match — e.g. query "fed 5.00%" where "fed" is in the title and "5.00%" is
// in a subtitle.
func eventMatchesDiscoveryQuery(event KalshiEvent, query string) bool {
	tokens := discoveryQueryTokens(query)
	if len(tokens) == 0 {
		return true
	}

	// Build a single searchable text pool from the event title and all
	// market subtitles. Tokens can be satisfied by any source.
	var pool strings.Builder
	pool.WriteString(strings.ToLower(event.Title))
	for _, m := range event.Markets {
		pool.WriteByte(' ')
		pool.WriteString(strings.ToLower(m.YesSubTitle))
	}
	text := pool.String()

	for _, tok := range tokens {
		// Year tokens (e.g. "2026") are optional in the event text filter.
		// Kalshi award/entertainment markets omit the year from their titles
		// ("Will X win Best Actor at the Oscars?" has no "2026"), so requiring
		// a year token in AND semantics would silently discard all such events.
		// The full-catalogue scan already excludes past markets via min_close_ts,
		// so skipping the year here does not admit stale results.
		if yearTokenRe.MatchString(tok) {
			continue
		}
		if strings.Contains(text, tok) {
			continue
		}
		// Allow synonym matches so "Solana" matches titles with "SOL", "march" matches "Mar",
		// "oscars" matches titles with "oscar", "winner" matches "win"/"wins".
		found := false
		for _, alt := range discoveryTokenSynonyms[tok] {
			if strings.Contains(text, alt) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
