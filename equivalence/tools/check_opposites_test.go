package tools_test

import (
	"testing"
	"time"

	"github.com/equinox/equivalence/tools"
	"github.com/equinox/models"
)

// makeMarket is a test helper that builds a minimal valid Market.
func makeMarket(title string, resolvesAt time.Time) models.Market {
	return models.Market{
		ID:         "test-id",
		Venue:      "kalshi",
		Title:      title,
		YesBid:     0.49,
		YesAsk:     0.51,
		YesMid:     0.50,
		NoPrice:    0.50,
		Spread:     0.02,
		Liquidity:  100000,
		Volume24h:  50000,
		ResolvesAt: resolvesAt,
		FetchedAt:  time.Now(),
		Status:     "open",
	}
}

func TestCheckOppositesDetectsPartyOpposites(t *testing.T) {
	tool := tools.NewCheckOpposites()
	future := time.Now().Add(180 * 24 * time.Hour)

	tests := []struct {
		name       string
		titleA     string
		titleB     string
		wantResult bool
	}{
		{
			name:       "gop vs democrats same race",
			titleA:     "will the gop control the house after 2026 midterms",
			titleB:     "will democrats control the house 2026",
			wantResult: true,
		},
		{
			name:       "republicans vs democratic party senate",
			titleA:     "will republicans win the senate 2026",
			titleB:     "will the democratic party win the senate 2026",
			wantResult: true,
		},
		{
			name:       "republican vs democrat president",
			titleA:     "republican wins the presidency 2028",
			titleB:     "democrat wins the presidency 2028",
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
			if result.Result && result.Confidence <= 0 {
				t.Errorf("Confidence = %v, want > 0 when result is true", result.Confidence)
			}
		})
	}
}

func TestCheckOppositesDetectsPriceOpposites(t *testing.T) {
	tool := tools.NewCheckOpposites()
	future := time.Now().Add(180 * 24 * time.Hour)

	tests := []struct {
		name       string
		titleA     string
		titleB     string
		wantResult bool
	}{
		{
			name:       "above vs below same price",
			titleA:     "will bitcoin close above 100000 in 2026",
			titleB:     "will bitcoin close below 100000 in 2026",
			wantResult: true,
		},
		{
			name:       "exceed vs under same asset",
			titleA:     "will sp500 exceed 6000 by year end",
			titleB:     "will sp500 fall under 6000 by year end",
			wantResult: true,
		},
		{
			name:       "over vs under same threshold",
			titleA:     "will ethereum trade over 5000 in 2026",
			titleB:     "will ethereum trade under 5000 in 2026",
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

func TestCheckOppositesDetectsYesNoOpposites(t *testing.T) {
	tool := tools.NewCheckOpposites()
	future := time.Now().Add(180 * 24 * time.Hour)

	tests := []struct {
		name       string
		titleA     string
		titleB     string
		wantResult bool
	}{
		{
			name:       "affirmative vs fail to",
			titleA:     "will the fed raise rates in 2026",
			titleB:     "will the fed fail to raise rates in 2026",
			wantResult: true,
		},
		{
			name:       "will vs will not",
			titleA:     "will the us enter a recession in 2026",
			titleB:     "will the us not enter a recession in 2026",
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

func TestCheckOppositesReturnsFalseUnrelated(t *testing.T) {
	tool := tools.NewCheckOpposites()
	future := time.Now().Add(180 * 24 * time.Hour)

	tests := []struct {
		name   string
		titleA string
		titleB string
	}{
		{
			name:   "crypto vs politics",
			titleA: "will bitcoin exceed 100k in 2026",
			titleB: "will democrats control house 2026",
		},
		{
			name:   "sports vs economics",
			titleA: "will the chiefs win the super bowl 2026",
			titleB: "will the fed cut rates in 2026",
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
				t.Errorf("Result = true for unrelated markets, want false (reasoning: %s)", result.Reasoning)
			}
		})
	}
}

func TestCheckOppositesConfidenceInRange(t *testing.T) {
	tool := tools.NewCheckOpposites()
	future := time.Now().Add(180 * 24 * time.Hour)

	pairs := [][2]string{
		{"will the gop control the house 2026", "will democrats control the house 2026"},
		{"will bitcoin close above 100000 in 2026", "will bitcoin close below 100000 in 2026"},
		{"will the fed raise rates in 2026", "will the fed fail to raise rates in 2026"},
		{"will bitcoin exceed 100k in 2026", "will democrats control house 2026"},
	}

	for _, pair := range pairs {
		marketA := makeMarket(pair[0], future)
		marketB := makeMarket(pair[1], future)
		result, err := tool.Execute(marketA, marketB)
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if result.Confidence < 0.0 || result.Confidence > 1.0 {
			t.Errorf("Confidence = %v out of [0.0, 1.0] for '%s' vs '%s'",
				result.Confidence, pair[0], pair[1])
		}
	}
}
