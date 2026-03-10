package tools

import (
	"fmt"
	"strings"

	"github.com/equinox/models"
)

// CheckStructural compares the structural question pattern of two market titles,
// independent of their specific content. The insight is that "Will X control Y?"
// and "Will X win Y?" have the same binary-outcome structure even when X and Y
// differ. If two markets share a structural pattern AND a domain category,
// that is evidence they are asking the same type of question.
//
// Structural categories:
//   - political-control : "will X control/win/take Y" about political bodies
//   - price-threshold   : "will X exceed/reach/hit/surpass/fall/drop N"
//   - event-occurrence  : "will X happen/occur/take place"
//   - rate-change       : "will X raise/cut/lower/increase/decrease rates"
type CheckStructural struct{}

// NewCheckStructural constructs a CheckStructural tool.
func NewCheckStructural() *CheckStructural { return &CheckStructural{} }

func (c *CheckStructural) Name() string { return "check_structural_equivalence" }

func (c *CheckStructural) Description() string {
	return "Compares question structure independent of content; 'Will X control Y?' ≅ 'Will X win Y?'"
}

// structuralPattern holds a category label and the keyword signals that identify it.
type structuralPattern struct {
	category string
	// All keywords in this slice that appear in a title contribute to matching.
	keywords []string
	// Domain words that narrow the category (empty = any domain).
	domainWords []string
}

var patterns = []structuralPattern{
	{
		category:    "political-control",
		keywords:    []string{"control", "win", "wins", "take", "takes", "secure", "hold", "holds", "gain", "gains", "flip"},
		domainWords: []string{"house", "senate", "seat", "seats", "majority", "election", "midterm", "midterms", "president", "presidency", "governor"},
	},
	{
		category:    "price-threshold",
		keywords:    []string{"exceed", "exceeds", "reach", "reaches", "hit", "hits", "surpass", "surpasses", "above", "below", "under", "over", "fall", "falls", "drop", "drops", "rise", "rises"},
		domainWords: []string{"bitcoin", "btc", "ethereum", "eth", "stock", "price", "index", "sp500", "nasdaq", "gold", "oil"},
	},
	{
		category:    "rate-change",
		keywords:    []string{"raise", "raises", "cut", "cuts", "lower", "lowers", "increase", "increases", "decrease", "decreases", "hike", "hikes", "pause", "pauses"},
		domainWords: []string{"rate", "rates", "interest", "fed", "federal", "reserve", "fomc", "bps", "basis"},
	},
	{
		category:    "event-occurrence",
		keywords:    []string{"happen", "happens", "occur", "occurs", "take place", "start", "starts", "end", "ends", "begin", "begins"},
		domainWords: []string{},
	},
}

// classifyTitle returns the structural pattern category for a title,
// or "" if no pattern matches.
func classifyTitle(title string) string {
	norm := strings.ToLower(title)
	for _, p := range patterns {
		if !containsAnyWord(norm, p.keywords) {
			continue
		}
		// If the pattern has domain words, at least one must appear.
		if len(p.domainWords) > 0 && !containsAnyWord(norm, p.domainWords) {
			continue
		}
		return p.category
	}
	return ""
}

func containsAnyWord(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// Execute classifies both titles and checks whether they share a structural category.
func (c *CheckStructural) Execute(marketA, marketB models.Market) (ToolResult, error) {
	catA := classifyTitle(marketA.Title)
	catB := classifyTitle(marketB.Title)

	if catA == "" || catB == "" {
		return ToolResult{
			ToolName:   "check_structural_equivalence",
			Result:     false,
			Confidence: 0.0,
			Reasoning:  "one or both titles did not match a known structural pattern",
		}, nil
	}

	if catA != catB {
		return ToolResult{
			ToolName:   "check_structural_equivalence",
			Result:     false,
			Confidence: 0.0,
			Reasoning: fmt.Sprintf(
				"structural mismatch: market A is '%s', market B is '%s'",
				catA, catB,
			),
		}, nil
	}

	return ToolResult{
		ToolName:   "check_structural_equivalence",
		Result:     true,
		Confidence: 0.80,
		Reasoning: fmt.Sprintf(
			"both markets match structural pattern '%s' — same question type",
			catA,
		),
	}, nil
}
