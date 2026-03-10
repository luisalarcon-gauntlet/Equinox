package tools_test

import (
	"testing"
	"time"

	"github.com/equinox/equivalence/tools"
)

func TestCheckEntityMatchHighOverlap(t *testing.T) {
	tool := tools.NewCheckEntityMatch()
	future := time.Now().Add(180 * 24 * time.Hour)

	tests := []struct {
		name        string
		titleA      string
		titleB      string
		wantResult  bool
		wantMinConf float64
	}{
		{
			name:        "identical titles — perfect overlap",
			titleA:      "will democrats control the house 2026",
			titleB:      "will democrats control the house 2026",
			wantResult:  true,
			wantMinConf: 0.90,
		},
		{
			name:        "same entities different word order",
			titleA:      "will democrats control house 2026",
			titleB:      "democrats win house control 2026",
			wantResult:  true,
			wantMinConf: 0.60,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marketA := makeMarket(tt.titleA, future)
			marketB := makeMarket(tt.titleB, future)
			result, err := tool.Execute(marketA, marketB)
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if result.Result != tt.wantResult {
				t.Errorf("Result = %v, want %v (reasoning: %s)", result.Result, tt.wantResult, result.Reasoning)
			}
			if tt.wantResult && result.Confidence < tt.wantMinConf {
				t.Errorf("Confidence = %v, want >= %v", result.Confidence, tt.wantMinConf)
			}
		})
	}
}

func TestCheckEntityMatchLowOverlap(t *testing.T) {
	tool := tools.NewCheckEntityMatch()
	future := time.Now().Add(180 * 24 * time.Hour)

	// These titles share year 2026 but little else — expect low confidence, no match.
	marketA := makeMarket("will bitcoin exceed 100000 in 2026", future)
	marketB := makeMarket("will democrats control the house 2026", future)

	result, err := tool.Execute(marketA, marketB)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// Low overlap means result should be false (Jaccard below threshold).
	if result.Result {
		t.Errorf("Result = true for low-overlap titles, want false (confidence: %.2f)", result.Confidence)
	}
	// Confidence should still be in range.
	if result.Confidence < 0.0 || result.Confidence > 1.0 {
		t.Errorf("Confidence out of range: %v", result.Confidence)
	}
}

func TestCheckEntityMatchNoOverlap(t *testing.T) {
	tool := tools.NewCheckEntityMatch()
	future := time.Now().Add(180 * 24 * time.Hour)

	marketA := makeMarket("will the chiefs win super bowl", future)
	marketB := makeMarket("will ethereum reach 10000", future)

	result, err := tool.Execute(marketA, marketB)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Result {
		t.Errorf("Result = true for no-overlap titles, want false")
	}
	if result.Confidence != 0.0 {
		t.Errorf("Confidence = %v for no-overlap, want 0.0", result.Confidence)
	}
}

func TestCheckEntityMatchExtractsNumbers(t *testing.T) {
	tool := tools.NewCheckEntityMatch()
	future := time.Now().Add(180 * 24 * time.Hour)

	// Both contain the same number — that should count as entity overlap.
	marketA := makeMarket("will bitcoin close above 100000 in 2026", future)
	marketB := makeMarket("will bitcoin fall below 100000 in 2026", future)

	result, err := tool.Execute(marketA, marketB)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// "bitcoin", "100000", "2026" overlap — Jaccard should be high.
	if !result.Result {
		t.Errorf("Result = false, want true — shared number entities should drive high overlap (confidence: %.2f)", result.Confidence)
	}
}

func TestCheckEntityMatchExtractsOrgs(t *testing.T) {
	tool := tools.NewCheckEntityMatch()
	future := time.Now().Add(180 * 24 * time.Hour)

	// Both reference the same organization.
	marketA := makeMarket("will apple reach market cap of 4 trillion 2026", future)
	marketB := makeMarket("will apple stock hit 300 by 2026", future)

	result, err := tool.Execute(marketA, marketB)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// "apple" and "2026" overlap — should produce a meaningful confidence score.
	if result.Confidence <= 0 {
		t.Errorf("Confidence = %v, want > 0 — shared org entity 'apple' + year '2026'", result.Confidence)
	}
}
