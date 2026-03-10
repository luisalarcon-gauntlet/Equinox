package kalshi

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// SeriesCache holds an in-memory index of all Kalshi series (the "Drawers"
// at the top of the exchange hierarchy). It is pre-warmed at startup and
// consulted on every search to convert a free-text query into a targeted
// list of series tickers — replacing the default full-catalogue scan.
//
// The cache is safe for concurrent reads and writes. A single RWMutex guards
// both the series slice and the timestamp; reads use RLock so concurrent
// searches never block each other.
type SeriesCache struct {
	mu        sync.RWMutex
	series    []KalshiSeries
	fetchedAt time.Time
	ttl       time.Duration
}

// newSeriesCache returns an empty (cold) SeriesCache with the given TTL.
func newSeriesCache(ttl time.Duration) *SeriesCache {
	return &SeriesCache{ttl: ttl}
}

// IsWarm reports whether the cache holds at least one series entry and has not
// exceeded its TTL. A cold or expired cache causes FetchMarkets to fall back
// to the full-catalogue scan so queries always return results.
func (sc *SeriesCache) IsWarm() bool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return len(sc.series) > 0 && time.Since(sc.fetchedAt) < sc.ttl
}

// populate replaces the cached series list and resets the TTL clock.
// Passing an empty slice is intentionally a no-op for the warmth check —
// an empty list means the API returned nothing useful and the fallback
// full-scan should remain active.
func (sc *SeriesCache) populate(series []KalshiSeries) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.series = series
	sc.fetchedAt = time.Now()
}

// minSeriesScore is the absolute score floor applied by RelevantSeries.
// A series must score at least this high to be fetched. The value 1.5
// requires at least two title-word matches (2 pts each × 2/n fraction)
// for any query with more than one token, eliminating single-token noise
// like a sports series matching only "winner" from "Oscars Best Actor Winner".
const minSeriesScore = 1.5

// relativeScoreFloor is the fraction of the top-scoring series' score that
// every retained series must meet. After sorting, any tail series whose score
// falls below (topScore × relativeScoreFloor) is dropped. This eliminates
// weak candidates that survived the absolute floor only because the query
// was short, while preserving Oscar-adjacent series that score reasonably
// close to the best match.
const relativeScoreFloor = 0.20

// RelevantSeries scores every cached series against query and returns those
// that exceed both a minimum absolute score and a relative floor based on the
// top-scoring series, sorted by score descending (best match first).
//
// Scoring weights per matched token:
//
//	Tag atom match  — 3 pts  (Kalshi-curated; highest signal)
//	Title word match — 2 pts (human-readable description)
//	Ticker synonym  — 1 pt  (fallback; e.g. "bitcoin" → "btc" in "KXBTCD")
//
// Two filters prevent low-quality tail matches from triggering HTTP requests:
//
//  1. Absolute floor (minSeriesScore): eliminates series that match only one
//     token out of a multi-token query. A single-token query is unaffected
//     because score = rawScore × 1.0, always exceeding the floor for any real match.
//
//  2. Relative floor (relativeScoreFloor): after sorting, any series scoring
//     below 20 % of the top-ranked series is dropped. This removes tail entries
//     that narrowly cleared the absolute floor but are disproportionately weak.
//
// An empty query (or a query that reduces to only stopwords) returns all
// cached series so that broad "show everything" requests still work.
//
// RelevantSeries returns nil when the cache is cold.
func (sc *SeriesCache) RelevantSeries(query string) []KalshiSeries {
	sc.mu.RLock()
	snapshot := make([]KalshiSeries, len(sc.series))
	copy(snapshot, sc.series)
	warm := len(sc.series) > 0 && time.Since(sc.fetchedAt) < sc.ttl
	sc.mu.RUnlock()

	if !warm {
		return nil
	}

	tokens := discoveryQueryTokens(query)
	if len(tokens) == 0 {
		// Empty / all-stopword query: return everything.
		return snapshot
	}

	type scored struct {
		s     KalshiSeries
		score float64
	}
	var matches []scored
	for _, s := range snapshot {
		if v := scoreSeriesForQuery(s, tokens); v >= minSeriesScore {
			matches = append(matches, scored{s, v})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})

	// Apply the relative floor: drop tail entries that score below
	// relativeScoreFloor × topScore. The list is already sorted descending,
	// so we can stop at the first entry that falls below the floor.
	if len(matches) > 0 {
		floor := matches[0].score * relativeScoreFloor
		cut := len(matches)
		for i, m := range matches {
			if m.score < floor {
				cut = i
				break
			}
		}
		matches = matches[:cut]
	}

	result := make([]KalshiSeries, len(matches))
	for i, m := range matches {
		result[i] = m.s
	}
	return result
}

// scoreSeriesForQuery returns a non-negative relevance score for series
// against the provided (already-stopword-filtered) query tokens.
//
// Each token is matched against three sources in priority order:
//  1. Tag atoms — the individual words extracted from Kalshi's curated tags
//     (e.g. "federal-reserve" expands to atoms "federal" and "reserve").
//  2. Title words — individual words from the series human-readable title.
//  3. Ticker synonyms — the same synonym table used by eventMatchesDiscoveryQuery
//     (e.g. "bitcoin" maps to "btc", matched against the series ticker).
//
// The raw per-token sum is multiplied by the fraction of tokens that matched
// at all. This ensures that a series matching 1 of 2 query tokens scores lower
// than a series matching 1 of 1 token with equal per-token weight, so
// RelevantSeries correctly ranks more-specific matches above partial ones.
//
// A nil or empty token list is treated as a match-all and returns 1.0.
func scoreSeriesForQuery(series KalshiSeries, tokens []string) float64 {
	if len(tokens) == 0 {
		return 1.0
	}

	tagAtoms := expandTagAtoms(series.Tags)
	titleWords := strings.Fields(strings.ToLower(series.Title))
	tickerLower := strings.ToLower(series.Ticker)

	rawScore := 0.0
	matched := 0
	for _, tok := range tokens {
		ts := matchToken(tok, tagAtoms, titleWords, tickerLower)
		if ts > 0 {
			matched++
		}
		rawScore += ts
	}
	if matched == 0 {
		return 0
	}
	// Multiply by match fraction so partial matches rank below full matches.
	return rawScore * (float64(matched) / float64(len(tokens)))
}

// matchToken returns the highest score achievable for a single query token
// across tags, title, and ticker synonyms.
func matchToken(tok string, tagAtoms, titleWords []string, tickerLower string) float64 {
	// Tag atom match (highest signal).
	for _, atom := range tagAtoms {
		if atom == tok || strings.Contains(atom, tok) {
			return 3.0
		}
	}

	// Title word match.
	for _, word := range titleWords {
		if word == tok || strings.Contains(word, tok) {
			return 2.0
		}
	}

	// Ticker synonym match (e.g. "bitcoin" → "btc" found in "KXBTCD").
	for _, alt := range discoveryTokenSynonyms[tok] {
		if strings.Contains(tickerLower, alt) {
			return 1.0
		}
	}

	return 0.0
}

// expandTagAtoms splits each tag on hyphens, underscores, and spaces to
// produce individual searchable atoms. The full unsplit tag is also included
// so an exact full-tag query still matches.
//
// Example: "federal-reserve" → ["federal-reserve", "federal", "reserve"]
func expandTagAtoms(tags []string) []string {
	var atoms []string
	for _, tag := range tags {
		lower := strings.ToLower(tag)
		atoms = append(atoms, lower) // full tag
		parts := strings.FieldsFunc(lower, func(r rune) bool {
			return r == '-' || r == '_' || r == ' '
		})
		atoms = append(atoms, parts...)
	}
	return atoms
}
