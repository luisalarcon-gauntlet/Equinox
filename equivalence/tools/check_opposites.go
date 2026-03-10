package tools

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/equinox/models"
)

// CheckOpposites detects when two markets are mirror images of the same event —
// opposite sides of the same binary outcome. Three categories are detected:
//  1. Political party opposites: GOP winning = Democrats losing on the same race.
//  2. Price/direction opposites: "above $X" vs "below $X" on the same asset.
//  3. Logical negation: "will X happen" vs "will X fail to happen".
type CheckOpposites struct{}

// NewCheckOpposites constructs a CheckOpposites tool.
func NewCheckOpposites() *CheckOpposites { return &CheckOpposites{} }

func (c *CheckOpposites) Name() string { return "check_opposites" }

func (c *CheckOpposites) Description() string {
	return "Detects when two markets are mirror images (opposite sides) of the same event."
}

// partyGroups maps each political party term to its opposing group identifier.
// Both sides of each pair share the same groupID so the check is symmetric.
var partyGroups = []struct {
	sideA []string
	sideB []string
	// context words that should appear in both titles to confirm same race
	context []string
}{
	{
		sideA:   []string{"gop", "republican", "republicans"},
		sideB:   []string{"democrat", "democrats", "democratic"},
		context: []string{"house", "senate", "president", "presidency", "election", "midterm", "midterms", "win", "control", "seat", "majority"},
	},
}

// directionPairs are word pairs where having one in A and the other in B
// (with shared subject words) signals a price/direction opposite.
var directionPairs = [][]string{
	{"above", "below"},
	{"exceed", "below"},
	{"exceed", "under"},
	{"over", "under"},
	{"higher", "lower"},
	{"rise", "fall"},
	{"gain", "lose"},
	{"win", "lose"},
	{"wins", "loses"},
}

// negationMarkers — if one title contains these and the other doesn't,
// combined with shared subject words, it's a logical-negation opposite.
var negationMarkers = []string{"fail to", "fails to", "not ", "won't", "will not", "doesn't", "does not"}

// Execute runs the opposite-detection logic and returns a ToolResult.
func (c *CheckOpposites) Execute(marketA, marketB models.Market) (ToolResult, error) {
	normA := normalizeTitle(marketA.Title)
	normB := normalizeTitle(marketB.Title)

	// Check political party opposites.
	if result, ok := checkPartyOpposites(normA, normB); ok {
		return result, nil
	}

	// Check price/direction opposites.
	if result, ok := checkDirectionOpposites(normA, normB); ok {
		return result, nil
	}

	// Check logical-negation opposites.
	if result, ok := checkNegationOpposites(normA, normB); ok {
		return result, nil
	}

	return ToolResult{
		ToolName:   "check_opposites",
		Result:     false,
		Confidence: 0.0,
		Reasoning:  "no opposite pattern detected between the two market titles",
	}, nil
}

// normalizeTitle lowercases and collapses whitespace but keeps all words
// (punctuation is converted to spaces so phrases like "fail to" are preserved).
func normalizeTitle(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func checkPartyOpposites(normA, normB string) (ToolResult, bool) {
	for _, group := range partyGroups {
		aHasSideA := containsAny(normA, group.sideA)
		aHasSideB := containsAny(normA, group.sideB)
		bHasSideA := containsAny(normB, group.sideA)
		bHasSideB := containsAny(normB, group.sideB)

		isOpposite := (aHasSideA && bHasSideB) || (aHasSideB && bHasSideA)
		if !isOpposite {
			continue
		}

		// Require shared context to confirm they're about the same race,
		// not just any two political titles.
		sharedCtx := sharedContextWords(normA, normB, group.context)
		if len(sharedCtx) == 0 {
			continue
		}

		return ToolResult{
			ToolName:   "check_opposites",
			Result:     true,
			Confidence: 0.90,
			Reasoning: fmt.Sprintf(
				"political party opposites detected — one market references one party, the other references the opposing party; shared context: %s",
				strings.Join(sharedCtx, ", "),
			),
		}, true
	}
	return ToolResult{}, false
}

func checkDirectionOpposites(normA, normB string) (ToolResult, bool) {
	for _, pair := range directionPairs {
		wordA, wordB := pair[0], pair[1]
		forward := strings.Contains(normA, wordA) && strings.Contains(normB, wordB)
		backward := strings.Contains(normA, wordB) && strings.Contains(normB, wordA)
		if !forward && !backward {
			continue
		}

		// Require that the titles share at least one non-direction content word
		// so we don't flag "will X exceed $5" vs "will Y fall under $3" as opposites.
		shared := sharedContentWords(normA, normB)
		if len(shared) == 0 {
			continue
		}

		return ToolResult{
			ToolName:   "check_opposites",
			Result:     true,
			Confidence: 0.85,
			Reasoning: fmt.Sprintf(
				"direction opposites detected (%q vs %q); shared subject words: %s",
				wordA, wordB, strings.Join(shared, ", "),
			),
		}, true
	}
	return ToolResult{}, false
}

func checkNegationOpposites(normA, normB string) (ToolResult, bool) {
	aHasNeg := containsAnyPhrase(normA, negationMarkers)
	bHasNeg := containsAnyPhrase(normB, negationMarkers)

	// Exactly one title should contain a negation marker.
	if aHasNeg == bHasNeg {
		return ToolResult{}, false
	}

	// Titles should otherwise share meaningful content words.
	shared := sharedContentWords(normA, normB)
	if len(shared) < 2 {
		return ToolResult{}, false
	}

	var negTitle, posTitle string
	if aHasNeg {
		negTitle, posTitle = normA, normB
	} else {
		negTitle, posTitle = normB, normA
	}

	return ToolResult{
		ToolName:   "check_opposites",
		Result:     true,
		Confidence: 0.80,
		Reasoning: fmt.Sprintf(
			"logical negation detected — '%s' is the negation of '%s'; shared words: %s",
			negTitle, posTitle, strings.Join(shared, ", "),
		),
	}, true
}

// containsAny returns true if s contains any of the given words as whole tokens.
func containsAny(s string, words []string) bool {
	tokens := strings.Fields(s)
	set := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		set[t] = struct{}{}
	}
	for _, w := range words {
		if _, ok := set[w]; ok {
			return true
		}
	}
	return false
}

// containsAnyPhrase returns true if s contains any of the given phrases as substrings.
func containsAnyPhrase(s string, phrases []string) bool {
	for _, p := range phrases {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// sharedContextWords returns the context words that appear in both titles.
func sharedContextWords(normA, normB string, context []string) []string {
	var shared []string
	for _, w := range context {
		if strings.Contains(normA, w) && strings.Contains(normB, w) {
			shared = append(shared, w)
		}
	}
	return shared
}

// sharedContentWords returns non-stop words that appear in both normalized titles.
func sharedContentWords(normA, normB string) []string {
	tokensA := contentTokens(normA)
	tokensB := contentTokens(normB)

	setB := make(map[string]struct{}, len(tokensB))
	for _, t := range tokensB {
		setB[t] = struct{}{}
	}

	seen := make(map[string]struct{})
	var shared []string
	for _, t := range tokensA {
		if _, inB := setB[t]; inB {
			if _, already := seen[t]; !already {
				shared = append(shared, t)
				seen[t] = struct{}{}
			}
		}
	}
	return shared
}

// stopWords contains grammatical/functional words excluded from entity extraction.
var stopWords = map[string]struct{}{
	"will": {}, "would": {}, "shall": {}, "should": {}, "can": {}, "could": {},
	"may": {}, "might": {}, "must": {}, "do": {}, "does": {}, "did": {},
	"the": {}, "a": {}, "an": {}, "this": {}, "that": {}, "these": {}, "those": {},
	"in": {}, "of": {}, "on": {}, "at": {}, "by": {}, "for": {}, "with": {},
	"to": {}, "from": {}, "into": {}, "after": {}, "before": {}, "over": {},
	"under": {}, "up": {}, "down": {}, "out": {}, "off": {}, "through": {},
	"about": {}, "against": {}, "and": {}, "or": {}, "but": {}, "nor": {},
	"so": {}, "yet": {}, "both": {}, "either": {}, "neither": {},
	"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {}, "being": {},
	"have": {}, "has": {}, "had": {}, "not": {}, "no": {},
	"it": {}, "its": {}, "what": {}, "which": {}, "who": {}, "how": {}, "when": {},
	"where": {}, "why": {},
}

func contentTokens(s string) []string {
	tokens := strings.Fields(s)
	var result []string
	for _, t := range tokens {
		if _, isStop := stopWords[t]; !isStop && len(t) > 1 {
			result = append(result, t)
		}
	}
	return result
}
