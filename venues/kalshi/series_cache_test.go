package kalshi

import (
	"testing"
	"time"
)

// ── scoreSeriesForQuery ───────────────────────────────────────────────────────

func TestScoreSeriesForQuery_NilTokensMatchesAll(t *testing.T) {
	s := KalshiSeries{Ticker: "KXBTCD", Title: "Bitcoin Daily Close", Tags: []string{"btc"}}
	score := scoreSeriesForQuery(s, nil)
	if score <= 0 {
		t.Errorf("nil tokens: want score > 0, got %f", score)
	}
}

func TestScoreSeriesForQuery_TagExactMatch(t *testing.T) {
	s := KalshiSeries{
		Ticker: "KXBTCD",
		Title:  "Bitcoin Daily Close",
		Tags:   []string{"bitcoin", "btc"},
	}
	score := scoreSeriesForQuery(s, []string{"bitcoin"})
	if score <= 0 {
		t.Errorf("tag exact match: want score > 0, got %f", score)
	}
}

func TestScoreSeriesForQuery_HyphenatedTagAtomMatch(t *testing.T) {
	// "federal-reserve" tag should match the token "federal".
	s := KalshiSeries{
		Ticker: "KXFED",
		Title:  "Fed Rate Decision",
		Tags:   []string{"federal-reserve", "interest-rates"},
	}
	score := scoreSeriesForQuery(s, []string{"federal"})
	if score <= 0 {
		t.Errorf("hyphenated tag atom match: want score > 0, got %f", score)
	}
}

func TestScoreSeriesForQuery_UnderscoreTagAtomMatch(t *testing.T) {
	s := KalshiSeries{
		Ticker: "KXCPI",
		Title:  "CPI Report",
		Tags:   []string{"consumer_price_index"},
	}
	score := scoreSeriesForQuery(s, []string{"consumer"})
	if score <= 0 {
		t.Errorf("underscore tag atom: want score > 0, got %f", score)
	}
}

func TestScoreSeriesForQuery_TitleSubstringMatch(t *testing.T) {
	// Tag list is empty; match must come from title words.
	s := KalshiSeries{
		Ticker: "KXFED",
		Title:  "Fed Rate Decision",
		Tags:   []string{},
	}
	score := scoreSeriesForQuery(s, []string{"rate"})
	if score <= 0 {
		t.Errorf("title word match: want score > 0, got %f", score)
	}
}

func TestScoreSeriesForQuery_SynonymMatchViaTicker(t *testing.T) {
	// "bitcoin" → synonym "btc" → contained in ticker "KXBTCD".
	s := KalshiSeries{
		Ticker: "KXBTCD",
		Title:  "Daily Close",
		Tags:   []string{},
	}
	score := scoreSeriesForQuery(s, []string{"bitcoin"})
	if score <= 0 {
		t.Errorf("ticker synonym match: want score > 0, got %f", score)
	}
}

func TestScoreSeriesForQuery_NoMatch(t *testing.T) {
	s := KalshiSeries{
		Ticker: "KXFED",
		Title:  "Fed Rate Decision",
		Tags:   []string{"federal-reserve"},
	}
	score := scoreSeriesForQuery(s, []string{"bitcoin"})
	if score != 0 {
		t.Errorf("no match: want 0, got %f", score)
	}
}

func TestScoreSeriesForQuery_TagScoresHigherThanTitleOnly(t *testing.T) {
	// Tag match should produce a higher score than a title-only match.
	tagSeries := KalshiSeries{
		Ticker: "KXTAG",
		Title:  "Unrelated Topic",
		Tags:   []string{"bitcoin"},
	}
	titleSeries := KalshiSeries{
		Ticker: "KXTITLE",
		Title:  "Bitcoin Something",
		Tags:   []string{},
	}
	tokens := []string{"bitcoin"}
	tagScore := scoreSeriesForQuery(tagSeries, tokens)
	titleScore := scoreSeriesForQuery(titleSeries, tokens)
	if tagScore <= titleScore {
		t.Errorf("tag score (%f) should exceed title-only score (%f)", tagScore, titleScore)
	}
}

func TestScoreSeriesForQuery_MultiTokenPartialMatch(t *testing.T) {
	// Series matches one of two tokens — score > 0 but less than a full match.
	s := KalshiSeries{
		Ticker: "KXBTCD",
		Title:  "Bitcoin Daily Close",
		Tags:   []string{"bitcoin"},
	}
	// "bitcoin" matches; "solana" does not.
	partial := scoreSeriesForQuery(s, []string{"bitcoin", "solana"})
	full := scoreSeriesForQuery(s, []string{"bitcoin"})
	if partial <= 0 {
		t.Errorf("partial match: want score > 0, got %f", partial)
	}
	if partial >= full {
		t.Errorf("partial (%f) should be less than full (%f)", partial, full)
	}
}

// ── SeriesCache ───────────────────────────────────────────────────────────────

func TestSeriesCache_ColdCacheIsNotWarm(t *testing.T) {
	sc := newSeriesCache(60 * time.Second)
	if sc.IsWarm() {
		t.Error("freshly created cache should not be warm")
	}
}

func TestSeriesCache_WarmAfterPopulate(t *testing.T) {
	sc := newSeriesCache(60 * time.Second)
	sc.populate([]KalshiSeries{{Ticker: "KXBTCD", Title: "Bitcoin"}})
	if !sc.IsWarm() {
		t.Error("cache should be warm immediately after populate")
	}
}

func TestSeriesCache_EmptyPopulateIsNotWarm(t *testing.T) {
	// Populating with zero series should not count as warm — it likely means
	// the API returned nothing and we have no useful index.
	sc := newSeriesCache(60 * time.Second)
	sc.populate([]KalshiSeries{})
	if sc.IsWarm() {
		t.Error("populate with empty slice should not mark cache as warm")
	}
}

func TestSeriesCache_ExpiredTTLIsNotWarm(t *testing.T) {
	sc := newSeriesCache(1 * time.Nanosecond)
	sc.populate([]KalshiSeries{{Ticker: "KXBTCD"}})
	time.Sleep(5 * time.Millisecond)
	if sc.IsWarm() {
		t.Error("TTL-expired cache should not be warm")
	}
}

func TestSeriesCache_RelevantSeriesEmptyQueryReturnsAll(t *testing.T) {
	sc := newSeriesCache(60 * time.Second)
	sc.populate([]KalshiSeries{
		{Ticker: "KXBTCD", Title: "Bitcoin"},
		{Ticker: "KXFED", Title: "Fed Rate"},
	})
	got := sc.RelevantSeries("")
	if len(got) != 2 {
		t.Errorf("empty query: want all 2 series, got %d", len(got))
	}
}

func TestSeriesCache_RelevantSeriesFiltersOutIrrelevant(t *testing.T) {
	sc := newSeriesCache(60 * time.Second)
	sc.populate([]KalshiSeries{
		{Ticker: "KXBTCD", Title: "Bitcoin Daily Close", Tags: []string{"bitcoin", "btc"}},
		{Ticker: "KXFED", Title: "Fed Rate Decision", Tags: []string{"federal-reserve"}},
	})
	got := sc.RelevantSeries("bitcoin")
	if len(got) != 1 {
		t.Fatalf("bitcoin query: want 1 series, got %d", len(got))
	}
	if got[0].Ticker != "KXBTCD" {
		t.Errorf("bitcoin query: want KXBTCD, got %s", got[0].Ticker)
	}
}

func TestSeriesCache_RelevantSeriesSortedByScoreDescending(t *testing.T) {
	sc := newSeriesCache(60 * time.Second)
	sc.populate([]KalshiSeries{
		// title-only match — lower score
		{Ticker: "KXLOW", Title: "Bitcoin News", Tags: []string{}},
		// tag match — higher score
		{Ticker: "KXHIGH", Title: "Daily Close", Tags: []string{"bitcoin", "btc"}},
	})
	got := sc.RelevantSeries("bitcoin")
	if len(got) != 2 {
		t.Fatalf("want 2 matches, got %d", len(got))
	}
	if got[0].Ticker != "KXHIGH" {
		t.Errorf("highest scorer should be first: want KXHIGH, got %s", got[0].Ticker)
	}
}

func TestSeriesCache_RelevantSeriesOnColdCacheReturnsEmpty(t *testing.T) {
	sc := newSeriesCache(60 * time.Second)
	got := sc.RelevantSeries("bitcoin")
	if len(got) != 0 {
		t.Errorf("cold cache: want 0 series, got %d", len(got))
	}
}

func TestSeriesCache_MultiplePopulatesReplacePrevious(t *testing.T) {
	sc := newSeriesCache(60 * time.Second)
	sc.populate([]KalshiSeries{{Ticker: "OLD", Title: "Old Series"}})
	sc.populate([]KalshiSeries{{Ticker: "NEW", Title: "New Series"}})
	got := sc.RelevantSeries("")
	if len(got) != 1 || got[0].Ticker != "NEW" {
		t.Errorf("second populate should replace first: got %v", got)
	}
}

// ── Threshold and relative-floor filtering ────────────────────────────────────

// TestSeriesCache_AbsoluteThresholdFiltersWeakMatches verifies that a series
// matching only one token out of three is eliminated by the absolute score
// floor, leaving only the strongly-matching series in the results.
func TestSeriesCache_AbsoluteThresholdFiltersWeakMatches(t *testing.T) {
	sc := newSeriesCache(60 * time.Second)
	sc.populate([]KalshiSeries{
		// Strong: "oscars" and "actor" both match as tags → score = 6 × (2/3) = 4.0
		{Ticker: "KXOSC", Title: "Oscar Best Actor", Tags: []string{"oscars", "actor"}},
		// Weak: only "actor" matches (title word) for a 3-token query → score = 2 × (1/3) ≈ 0.67
		{Ticker: "KXWEAK", Title: "Best Actor Award", Tags: []string{"sports"}},
	})
	got := sc.RelevantSeries("oscars actor winner")
	if len(got) != 1 {
		t.Fatalf("absolute threshold should filter KXWEAK; want 1 result, got %d", len(got))
	}
	if got[0].Ticker != "KXOSC" {
		t.Errorf("want KXOSC, got %s", got[0].Ticker)
	}
}

// TestSeriesCache_RelativeFloorFiltersWeakTail verifies that a series which
// clears the absolute threshold but scores far below the top-ranked series is
// eliminated by the relative score floor. This prevents hundreds of weak
// tail-matches from triggering HTTP requests when a strong match exists.
//
// Score math (5-token query: "oscars", "2026", "best", "actor", "winner"):
//
//	KXSTRONG: tags match "oscars","best","actor","winner" → rawScore=12, 4/5 → 9.6
//	KXWEAK:   title matches "best","actor" only            → rawScore=4,  2/5 → 1.6
//
// relativeFloor = 9.6 × 0.20 = 1.92 → 1.6 < 1.92 → KXWEAK eliminated.
func TestSeriesCache_RelativeFloorFiltersWeakTail(t *testing.T) {
	sc := newSeriesCache(60 * time.Second)
	sc.populate([]KalshiSeries{
		{Ticker: "KXSTRONG", Title: "Oscar Best Actor Winner", Tags: []string{"oscars", "best", "actor", "winner"}},
		{Ticker: "KXWEAK", Title: "Best Actor Award", Tags: []string{"sports"}},
	})
	got := sc.RelevantSeries("oscars 2026 best actor winner")
	if len(got) != 1 {
		t.Fatalf("relative floor should eliminate KXWEAK; want 1 result, got %d", len(got))
	}
	if got[0].Ticker != "KXSTRONG" {
		t.Errorf("want KXSTRONG, got %s", got[0].Ticker)
	}
}
