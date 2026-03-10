package tools_test

import (
	"testing"
	"time"

	"github.com/equinox/equivalence/tools"
)

func TestCheckStructuralSamePattern(t *testing.T) {
	tool := tools.NewCheckStructural()
	future := time.Now().Add(180 * 24 * time.Hour)

	tests := []struct {
		name        string
		titleA      string
		titleB      string
		wantResult  bool
		wantMinConf float64
	}{
		{
			name:        "both will-control questions",
			titleA:      "will democrats control the house 2026",
			titleB:      "will republicans control the senate 2026",
			wantResult:  true,
			wantMinConf: 0.70,
		},
		{
			name:        "will-win and will-win same structure",
			titleA:      "will the gop win the senate 2026",
			titleB:      "will democrats win the senate 2026",
			wantResult:  true,
			wantMinConf: 0.70,
		},
		{
			name:        "will-exceed and will-exceed price questions",
			titleA:      "will bitcoin exceed 100000 by december 2026",
			titleB:      "will ethereum exceed 10000 by december 2026",
			wantResult:  true,
			wantMinConf: 0.70,
		},
		{
			name:        "will-reach and will-hit threshold questions",
			titleA:      "will bitcoin reach 200000 in 2026",
			titleB:      "will bitcoin hit 200000 in 2026",
			wantResult:  true,
			wantMinConf: 0.70,
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

func TestCheckStructuralDifferentPatterns(t *testing.T) {
	tool := tools.NewCheckStructural()
	future := time.Now().Add(180 * 24 * time.Hour)

	tests := []struct {
		name   string
		titleA string
		titleB string
	}{
		{
			name:   "control-question vs price-threshold question",
			titleA: "will democrats control the house 2026",
			titleB: "will bitcoin exceed 100000 in 2026",
		},
		{
			name:   "political win vs sports win",
			titleA: "will republicans win the senate 2026",
			titleB: "will the chiefs win the super bowl 2026",
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
			// Different domains — even if the structure partially matches,
			// the tool should return false or low confidence.
			if result.Result && result.Confidence > 0.5 {
				t.Errorf("Result = true with high confidence for different-domain questions — want false or low confidence")
			}
		})
	}
}

func TestCheckStructuralConfidenceInRange(t *testing.T) {
	tool := tools.NewCheckStructural()
	future := time.Now().Add(180 * 24 * time.Hour)

	pairs := [][2]string{
		{"will democrats control the house 2026", "will republicans control the senate 2026"},
		{"will bitcoin exceed 100000 in 2026", "will bitcoin fall below 100000 in 2026"},
		{"will the chiefs win the super bowl 2026", "will the fed cut rates in 2026"},
	}

	for _, pair := range pairs {
		marketA := makeMarket(pair[0], future)
		marketB := makeMarket(pair[1], future)
		result, err := tool.Execute(marketA, marketB)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Confidence < 0.0 || result.Confidence > 1.0 {
			t.Errorf("Confidence = %v out of [0.0, 1.0]", result.Confidence)
		}
	}
}
