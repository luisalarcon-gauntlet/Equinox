package tools

import (
	"fmt"
	"strings"

	"github.com/equinox/models"
)

// CheckSynonyms detects equivalent terminology used differently across venues.
// It uses a manually curated synonym map covering political, financial, and
// geographic terms that are commonly abbreviated or aliased on prediction markets.
type CheckSynonyms struct{}

// NewCheckSynonyms constructs a CheckSynonyms tool.
func NewCheckSynonyms() *CheckSynonyms { return &CheckSynonyms{} }

func (c *CheckSynonyms) Name() string { return "check_synonyms" }

func (c *CheckSynonyms) Description() string {
	return "Detects equivalent terminology used differently across venues (GOP=Republicans, Fed=Federal Reserve, etc.)."
}

// synonymGroup is a set of terms that all mean the same thing.
// If marketA contains any term from a group and marketB contains a different
// term from the same group, that is a synonym hit.
type synonymGroup struct {
	label string
	terms []string
}

// synonymGroups is the curated synonym dictionary.
// Each group's terms are lowercased; matching is always case-insensitive.
var synonymGroups = []synonymGroup{
	{
		label: "Republican Party",
		terms: []string{"gop", "republican", "republicans", "republican party"},
	},
	{
		label: "Democratic Party",
		terms: []string{"dems", "dem", "democrat", "democrats", "democratic", "democratic party"},
	},
	{
		label: "Federal Reserve",
		terms: []string{"fed", "federal reserve", "fomc", "central bank"},
	},
	{
		label: "Bitcoin",
		terms: []string{"btc", "bitcoin"},
	},
	{
		label: "Ethereum",
		terms: []string{"eth", "ethereum"},
	},
	{
		label: "United States",
		terms: []string{"us", "usa", "united states", "america", "american"},
	},
	{
		label: "Control/Win",
		terms: []string{"win", "wins", "control", "controls", "take", "takes", "secure", "secures"},
	},
	{
		label: "Rate reduction",
		terms: []string{"rate cut", "rate cuts", "lower rates", "cut rates", "reduce rates", "rate reduction"},
	},
}

// SynonymHit records a matched synonym pair for reporting.
type SynonymHit struct {
	Group string
	TermA string
	TermB string
}

// Execute scans both market titles for synonym pairs and returns a ToolResult.
func (c *CheckSynonyms) Execute(marketA, marketB models.Market) (ToolResult, error) {
	normA := strings.ToLower(marketA.Title)
	normB := strings.ToLower(marketB.Title)

	var hits []SynonymHit

	for _, group := range synonymGroups {
		termInA := matchedTerm(normA, group.terms)
		termInB := matchedTerm(normB, group.terms)

		// Both titles reference the same synonym group but with different terms.
		if termInA != "" && termInB != "" && termInA != termInB {
			hits = append(hits, SynonymHit{
				Group: group.label,
				TermA: termInA,
				TermB: termInB,
			})
		}
	}

	if len(hits) == 0 {
		return ToolResult{
			ToolName:   "check_synonyms",
			Result:     false,
			Confidence: 0.0,
			Reasoning:  "no synonym pairs found between the two market titles",
		}, nil
	}

	confidence := synonymConfidence(len(hits))
	reasoning := buildSynonymReasoning(hits)

	return ToolResult{
		ToolName:   "check_synonyms",
		Result:     true,
		Confidence: confidence,
		Reasoning:  reasoning,
	}, nil
}

// matchedTerm returns the longest term from the candidate list found in s,
// or the empty string if none match. Longest-first matching prevents "fed"
// from shadowing "federal reserve" when both appear as candidates.
func matchedTerm(s string, candidates []string) string {
	// Sort longest first so specific multi-word terms win over short aliases.
	sorted := make([]string, len(candidates))
	copy(sorted, candidates)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if len(sorted[j]) > len(sorted[i]) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	for _, term := range sorted {
		if strings.Contains(s, term) {
			return term
		}
	}
	return ""
}

// synonymConfidence returns a confidence score that grows with the number of
// synonym hits but is capped at 0.95 (AI synthesis still makes the final call).
func synonymConfidence(hitCount int) float64 {
	switch {
	case hitCount >= 3:
		return 0.95
	case hitCount == 2:
		return 0.85
	default:
		return 0.75
	}
}

func buildSynonymReasoning(hits []SynonymHit) string {
	parts := make([]string, len(hits))
	for i, h := range hits {
		parts[i] = fmt.Sprintf("%q ↔ %q (%s)", h.TermA, h.TermB, h.Group)
	}
	return fmt.Sprintf("synonym pairs found: %s", strings.Join(parts, "; "))
}
