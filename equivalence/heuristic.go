package equivalence

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/equinox/models"
)

// HeuristicMatcher runs the deterministic first-pass equivalence check.
//
// Algorithm (from EQUIVALENCE.md):
//  1. Normalize both titles (lowercase, strip punctuation, collapse whitespace).
//  2. Extract content entities (non-stop words + numeric tokens).
//  3. Score entity overlap with Jaccard similarity.
//  4. Score date proximity on a fixed scale.
//  5. Combined confidence = (entity * 0.60) + (date * 0.40).
//
// If confidence >= threshold → IsMatch=true, Method="heuristic".
// If confidence <  threshold → IsMatch=false; the Detector escalates to the AI layer.
type HeuristicMatcher struct {
	threshold float64
}

// NewHeuristicMatcher creates a HeuristicMatcher with the given confidence threshold.
// The canonical threshold is 0.80 (from config.HeuristicConfidenceThreshold).
func NewHeuristicMatcher(threshold float64) *HeuristicMatcher {
	return &HeuristicMatcher{threshold: threshold}
}

// Match runs the heuristic algorithm and returns a MatchResult.
// It never errors — any bad input (empty titles, zero dates) is handled gracefully.
func (h *HeuristicMatcher) Match(marketA, marketB models.Market) models.MatchResult {
	normA := normalizeHeuristic(marketA.Title)
	normB := normalizeHeuristic(marketB.Title)

	entitiesA := heuristicEntities(normA)
	entitiesB := heuristicEntities(normB)

	entityScore := heuristicJaccard(entitiesA, entitiesB)
	dateScore := heuristicDateScore(marketA.ResolvesAt, marketB.ResolvesAt)

	confidence := (entityScore * 0.60) + (dateScore * 0.40)
	// Clamp to [0, 1] to guard against floating point drift.
	confidence = math.Min(1.0, math.Max(0.0, confidence))

	isMatch := confidence >= h.threshold
	reasoning := buildHeuristicReasoning(entitiesA, entitiesB, entityScore, dateScore, confidence, h.threshold)

	return models.MatchResult{
		MarketA:    marketA,
		MarketB:    marketB,
		IsMatch:    isMatch,
		Confidence: confidence,
		Method:     "heuristic",
		Reasoning:  reasoning,
		MatchedAt:  time.Now(),
	}
}

// normalizeHeuristic lowercases s, converts punctuation to spaces, and
// collapses runs of whitespace to single spaces.
func normalizeHeuristic(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// heuristicStopWords contains grammatical / functional words excluded from
// entity extraction. Domain verbs (control, win, cut) are kept because they
// carry semantic meaning for prediction market titles.
var heuristicStopWords = map[string]struct{}{
	"will": {}, "would": {}, "shall": {}, "should": {}, "can": {}, "could": {},
	"may": {}, "might": {}, "must": {}, "do": {}, "does": {}, "did": {},
	"the": {}, "a": {}, "an": {}, "this": {}, "that": {}, "these": {}, "those": {},
	"in": {}, "of": {}, "on": {}, "at": {}, "by": {}, "for": {}, "with": {},
	"to": {}, "from": {}, "into": {}, "after": {}, "before": {},
	"through": {}, "about": {}, "against": {},
	"and": {}, "or": {}, "but": {}, "nor": {}, "so": {}, "yet": {},
	"both": {}, "either": {}, "neither": {},
	"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
	"have": {}, "has": {}, "had": {}, "not": {}, "no": {},
	"it": {}, "its": {}, "what": {}, "which": {}, "who": {}, "how": {},
	"when": {}, "where": {}, "why": {},
}

// heuristicEntities extracts meaningful tokens from a normalized title.
// Returns a set (map) of strings for Jaccard computation.
func heuristicEntities(normalized string) map[string]struct{} {
	tokens := strings.Fields(normalized)
	result := make(map[string]struct{})
	for _, tok := range tokens {
		if _, isStop := heuristicStopWords[tok]; isStop {
			continue
		}
		if len(tok) > 1 {
			result[tok] = struct{}{}
		}
	}
	return result
}

// heuristicJaccard computes |A ∩ B| / |A ∪ B|.
// Returns 0.0 when both sets are empty.
func heuristicJaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0.0
	}
	var intersection int
	for k := range a {
		if _, ok := b[k]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}

// heuristicDateScore maps the absolute day difference between two dates to
// a proximity score in [0, 1]. This mirrors the scoring in check_date_alignment.go
// so results are consistent across both layers.
//
//   - Zero dates (missing/unparsed)      : 0.0
//   - Same day                           : 1.0
//   - Within 1 day                       : 0.90
//   - Within 7 days                      : 0.70
//   - Within 30 days                     : 0.40
//   - More than 30 days apart            : 0.0
func heuristicDateScore(a, b time.Time) float64 {
	if a.IsZero() || b.IsZero() {
		return 0.0
	}
	daysApart := int(math.Round(math.Abs(a.Sub(b).Hours() / 24)))
	switch {
	case daysApart == 0:
		return 1.0
	case daysApart <= 1:
		return 0.90
	case daysApart <= 7:
		return 0.70
	case daysApart <= 30:
		return 0.40
	default:
		return 0.0
	}
}

// tokenSet converts a normalized string into a set of its whitespace-delimited
// tokens with no stop-word filtering. Used by DebugTopPairs to compute raw
// token overlap before entity extraction so we can see how much stop-word
// removal moves the score.
func tokenSet(normalized string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, t := range strings.Fields(normalized) {
		set[t] = struct{}{}
	}
	return set
}

// DebugTopPairs scores every pair in marketsA × marketsB, sorts by combined
// heuristic confidence, and prints the top topN entries to stderr.
//
// Output per pair:
//
//	[DEBUG] pair #N score=X.XX
//	  <venueA>:    "<raw title A>"
//	  <venueB>:    "<raw title B>"
//	  normalized_<keyA>:  "<normalized A>"
//	  normalized_<keyB>:  "<normalized B>"
//	  token_overlap: X.XX  entity_match: X.XX  date_match: X.XX
//
// token_overlap = Jaccard of ALL normalised tokens (no stop-word filter).
// entity_match  = Jaccard of entity tokens (stop words removed) — this is the
//
//	value that drives the combined confidence score.
//
// date_match    = heuristicDateScore result.
//
// THIS IS TEMPORARY DEBUG INSTRUMENTATION — remove before any production use.
func (h *HeuristicMatcher) DebugTopPairs(marketsA, marketsB []models.Market, topN int) {
	type pairScore struct {
		a, b         models.Market
		normA, normB string
		tokenOverlap float64
		entityMatch  float64
		dateMatch    float64
		combined     float64
	}

	pairs := make([]pairScore, 0, len(marketsA)*len(marketsB))
	for _, a := range marketsA {
		for _, b := range marketsB {
			nA := normalizeHeuristic(a.Title)
			nB := normalizeHeuristic(b.Title)

			tokOverlap := heuristicJaccard(tokenSet(nA), tokenSet(nB))
			entMatch := heuristicJaccard(heuristicEntities(nA), heuristicEntities(nB))
			dateSc := heuristicDateScore(a.ResolvesAt, b.ResolvesAt)
			combined := math.Min(1.0, math.Max(0.0, entMatch*0.60+dateSc*0.40))

			pairs = append(pairs, pairScore{
				a: a, b: b,
				normA: nA, normB: nB,
				tokenOverlap: tokOverlap,
				entityMatch:  entMatch,
				dateMatch:    dateSc,
				combined:     combined,
			})
		}
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].combined > pairs[j].combined
	})

	if topN > len(pairs) {
		topN = len(pairs)
	}

	fmt.Fprintf(os.Stderr, "[DEBUG] DebugTopPairs — %d total pairs, showing top %d\n", len(pairs), topN)
	for i := 0; i < topN; i++ {
		p := pairs[i]

		venueA := p.a.Venue
		venueB := p.b.Venue
		keyA := venueShortKey(venueA)
		keyB := venueShortKey(venueB)

		fmt.Fprintf(os.Stderr,
			"[DEBUG] pair #%d score=%.2f\n  %-12s%q\n  %-12s%q\n  normalized_%s:  %q\n  normalized_%s:  %q\n  token_overlap: %.2f  entity_match: %.2f  date_match: %.2f\n",
			i+1, p.combined,
			venueA+":", p.a.Title,
			venueB+":", p.b.Title,
			keyA, p.normA,
			keyB, p.normB,
			p.tokenOverlap, p.entityMatch, p.dateMatch,
		)
	}
}

// venueShortKey returns the single-character abbreviation used in debug label
// suffixes: "k" for "kalshi", "p" for "polymarket", otherwise the first rune.
func venueShortKey(venue string) string {
	switch venue {
	case "kalshi":
		return "k"
	case "polymarket":
		return "p"
	default:
		if len(venue) == 0 {
			return "?"
		}
		return string([]rune(venue)[0])
	}
}

func buildHeuristicReasoning(a, b map[string]struct{}, entityScore, dateScore, confidence, threshold float64) string {
	var shared []string
	for k := range a {
		if _, ok := b[k]; ok {
			shared = append(shared, k)
		}
	}
	sharedStr := strings.Join(shared, ", ")
	if sharedStr == "" {
		sharedStr = "none"
	}
	verdict := "below threshold — escalating to AI layer"
	if confidence >= threshold {
		verdict = "above threshold — match confirmed"
	}
	return fmt.Sprintf(
		"entity overlap: %.2f (shared: %s), date proximity: %.2f → combined: %.2f — %s",
		entityScore, sharedStr, dateScore, confidence, verdict,
	)
}
