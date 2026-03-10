package tools_test

import (
	"testing"
	"time"

	"github.com/equinox/equivalence/tools"
)

func TestCheckSynonymsDetectsPartyNames(t *testing.T) {
	tool := tools.NewCheckSynonyms()
	future := time.Now().Add(180 * 24 * time.Hour)

	tests := []struct {
		name       string
		titleA     string
		titleB     string
		wantResult bool
	}{
		{
			name:       "GOP equals Republicans",
			titleA:     "will the gop win the house 2026",
			titleB:     "will republicans win the house 2026",
			wantResult: true,
		},
		{
			name:       "Dems equals Democrats",
			titleA:     "will dems take control of senate 2026",
			titleB:     "will democrats take control of senate 2026",
			wantResult: true,
		},
		{
			name:       "GOP vs Democratic Party (same synonym group)",
			titleA:     "gop wins presidency 2028",
			titleB:     "republican party wins presidency 2028",
			wantResult: true,
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
		})
	}
}

func TestCheckSynonymsDetectsFinancialTerms(t *testing.T) {
	tool := tools.NewCheckSynonyms()
	future := time.Now().Add(180 * 24 * time.Hour)

	tests := []struct {
		name       string
		titleA     string
		titleB     string
		wantResult bool
	}{
		{
			name:       "Fed equals Federal Reserve",
			titleA:     "will the fed cut rates in 2026",
			titleB:     "will the federal reserve cut rates in 2026",
			wantResult: true,
		},
		{
			name:       "BTC equals Bitcoin",
			titleA:     "will btc reach 200000 in 2026",
			titleB:     "will bitcoin reach 200000 in 2026",
			wantResult: true,
		},
		{
			name:       "ETH equals Ethereum",
			titleA:     "will eth exceed 10000 by december 2026",
			titleB:     "will ethereum exceed 10000 by december 2026",
			wantResult: true,
		},
		{
			name:       "US equals United States",
			titleA:     "will the us enter recession 2026",
			titleB:     "will the united states enter recession 2026",
			wantResult: true,
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
		})
	}
}

func TestCheckSynonymsCaseInsensitive(t *testing.T) {
	tool := tools.NewCheckSynonyms()
	future := time.Now().Add(180 * 24 * time.Hour)

	// Titles with mixed case that the adapter would normalize to lowercase.
	// Since market titles are stored normalized, we use lowercase here —
	// but verify the synonym lookup works regardless of input casing within the tool.
	marketA := makeMarket("will the Fed cut interest rates in 2026", future)
	marketB := makeMarket("will the FEDERAL RESERVE cut interest rates in 2026", future)

	result, err := tool.Execute(marketA, marketB)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Result {
		t.Errorf("Result = false, want true — synonym lookup must be case-insensitive")
	}
}

func TestCheckSynonymsReturnsFalseNoSynonyms(t *testing.T) {
	tool := tools.NewCheckSynonyms()
	future := time.Now().Add(180 * 24 * time.Hour)

	tests := []struct {
		name   string
		titleA string
		titleB string
	}{
		{
			name:   "completely unrelated topics",
			titleA: "will the chiefs win the super bowl 2026",
			titleB: "will bitcoin exceed 100000 in 2026",
		},
		{
			name:   "same topic no synonym pairs",
			titleA: "will apple stock reach 300 in 2026",
			titleB: "will apple shares hit 300 in 2026",
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
			if result.Result {
				t.Errorf("Result = true, want false — no synonym pairs in these titles")
			}
		})
	}
}
