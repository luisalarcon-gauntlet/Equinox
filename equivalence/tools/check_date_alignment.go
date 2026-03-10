package tools

import (
	"fmt"
	"math"

	"github.com/equinox/models"
)

// CheckDateAlignment compares the resolution dates of two markets.
// Markets resolving within a configurable window are considered aligned.
// Missing (zero) dates are handled gracefully — they produce Result=false
// with Confidence=0.0 rather than an error, because a missing date is a
// data quality issue, not a logic failure.
type CheckDateAlignment struct{}

// NewCheckDateAlignment constructs a CheckDateAlignment tool.
func NewCheckDateAlignment() *CheckDateAlignment { return &CheckDateAlignment{} }

func (c *CheckDateAlignment) Name() string { return "check_date_alignment" }

func (c *CheckDateAlignment) Description() string {
	return "Compares market resolution dates; aligned within 7 days = confident match."
}

// Execute computes the number of days between the two resolution dates and
// returns a confidence score based on proximity.
func (c *CheckDateAlignment) Execute(marketA, marketB models.Market) (ToolResult, error) {
	if marketA.ResolvesAt.IsZero() || marketB.ResolvesAt.IsZero() {
		return ToolResult{
			ToolName:   "check_date_alignment",
			Result:     false,
			Confidence: 0.0,
			Reasoning:  "one or both markets are missing a resolution date — alignment cannot be confirmed",
		}, nil
	}

	daysApart := int(math.Round(math.Abs(marketA.ResolvesAt.Sub(marketB.ResolvesAt).Hours() / 24)))
	confidence := dateProximityScore(daysApart)
	isAligned := confidence >= 0.70

	return ToolResult{
		ToolName:   "check_date_alignment",
		Result:     isAligned,
		Confidence: confidence,
		Reasoning: fmt.Sprintf(
			"resolution dates are %d day(s) apart (A: %s, B: %s) — confidence: %.2f",
			daysApart,
			marketA.ResolvesAt.Format("2006-01-02"),
			marketB.ResolvesAt.Format("2006-01-02"),
			confidence,
		),
	}, nil
}

// dateProximityScore returns a confidence in [0, 1] based on how close two
// resolution dates are. This mirrors the scoring used in the heuristic layer
// so the AI layer can weight date evidence consistently.
//
//   - Exact same date : 1.0
//   - Within 1 day   : 0.90
//   - Within 7 days  : 0.70
//   - Within 30 days : 0.40
//   - Beyond 30 days : 0.0
func dateProximityScore(daysApart int) float64 {
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
