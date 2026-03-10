package tools

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/equinox/models"
)

// CheckEntityMatch extracts named entities from both market titles and scores
// overlap using the Jaccard similarity coefficient.
// Entities include: numeric tokens (years, prices), known organization names,
// and all non-stop content words.
//
// This is the tool-layer equivalent of the heuristic entity check but runs
// after synonym normalization, so it catches cases where "GOP" and "Republicans"
// were resolved to the same group before counting overlap.
type CheckEntityMatch struct{}

// NewCheckEntityMatch constructs a CheckEntityMatch tool.
func NewCheckEntityMatch() *CheckEntityMatch { return &CheckEntityMatch{} }

func (c *CheckEntityMatch) Name() string { return "check_entity_match" }

func (c *CheckEntityMatch) Description() string {
	return "Scores named-entity overlap (Jaccard similarity) between two market titles."
}

// entityMatchThreshold is the minimum Jaccard score for Result=true.
const entityMatchThreshold = 0.40

// Execute extracts entities from both titles and computes Jaccard similarity.
func (c *CheckEntityMatch) Execute(marketA, marketB models.Market) (ToolResult, error) {
	entitiesA := extractEntities(normalizeTitle(marketA.Title))
	entitiesB := extractEntities(normalizeTitle(marketB.Title))

	score := jaccardSimilarity(entitiesA, entitiesB)
	isMatch := score >= entityMatchThreshold

	reasoning := buildEntityReasoning(entitiesA, entitiesB, score)

	return ToolResult{
		ToolName:   "check_entity_match",
		Result:     isMatch,
		Confidence: score,
		Reasoning:  reasoning,
	}, nil
}

// extractEntities returns the set of meaningful tokens from a normalized title.
// A token is "meaningful" if it is not a stop word and has length > 1.
// All numeric tokens (years, prices like "100000", "100k") are always included.
func extractEntities(normalized string) map[string]struct{} {
	tokens := strings.Fields(normalized)
	entities := make(map[string]struct{})
	for _, tok := range tokens {
		if isNumericToken(tok) {
			entities[tok] = struct{}{}
			continue
		}
		if _, isStop := stopWords[tok]; isStop {
			continue
		}
		if len(tok) > 1 {
			entities[tok] = struct{}{}
		}
	}
	return entities
}

// isNumericToken returns true if the token is purely numeric or a common
// abbreviated number (e.g. "100k", "2m", "4t").
func isNumericToken(tok string) bool {
	if tok == "" {
		return false
	}
	for i, r := range tok {
		if !unicode.IsDigit(r) {
			// Allow a single trailing suffix on the last character.
			if i == len([]rune(tok))-1 {
				switch r {
				case 'k', 'm', 'b', 't':
					return true
				}
			}
			return false
		}
	}
	return true
}

// jaccardSimilarity computes |A ∩ B| / |A ∪ B| for two entity sets.
// Returns 0.0 when both sets are empty (no meaningful entities found).
func jaccardSimilarity(a, b map[string]struct{}) float64 {
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

func buildEntityReasoning(a, b map[string]struct{}, score float64) string {
	var shared []string
	for k := range a {
		if _, ok := b[k]; ok {
			shared = append(shared, k)
		}
	}
	if len(shared) == 0 {
		return fmt.Sprintf("entity overlap score: %.2f — no shared entities found", score)
	}
	return fmt.Sprintf(
		"entity overlap score: %.2f (Jaccard) — shared entities: %s",
		score, strings.Join(shared, ", "),
	)
}
