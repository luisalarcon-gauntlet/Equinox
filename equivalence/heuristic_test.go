package equivalence_test

import (
	"testing"
	"time"

	"github.com/equinox/equivalence"
	"github.com/equinox/models"
)

// heuristicTestMarket builds a market for heuristic tests.
func heuristicTestMarket(title string, resolvesAt time.Time) models.Market {
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

func TestHeuristicMatchesIdenticalTitles(t *testing.T) {
	h := equivalence.NewHeuristicMatcher(0.80)
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

	title := "will democrats control the house 2026"
	marketA := heuristicTestMarket(title, date)
	marketB := heuristicTestMarket(title, date)

	result := h.Match(marketA, marketB)

	if !result.IsMatch {
		t.Errorf("IsMatch = false for identical titles, want true")
	}
	if result.Confidence < 0.95 {
		t.Errorf("Confidence = %v for identical titles, want >= 0.95", result.Confidence)
	}
	if result.Method != "heuristic" {
		t.Errorf("Method = %q, want 'heuristic'", result.Method)
	}
	if result.Reasoning == "" {
		t.Errorf("Reasoning must not be empty")
	}
}

func TestHeuristicMatchesSimilarTitles(t *testing.T) {
	h := equivalence.NewHeuristicMatcher(0.80)
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		titleA      string
		titleB      string
		wantMatch   bool
		wantMinConf float64
	}{
		{
			// Same entities, only stop words differ — Jaccard should be ~1.0
			name:        "same entities different stop words",
			titleA:      "will democrats control house 2026",
			titleB:      "democrats control house 2026",
			wantMatch:   true,
			wantMinConf: 0.80,
		},
		{
			// High entity overlap: bitcoin, 100000, 2026 in both
			name:        "same asset same price same year",
			titleA:      "will bitcoin close above 100000 in 2026",
			titleB:      "will bitcoin close below 100000 in 2026",
			wantMatch:   true,
			wantMinConf: 0.80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marketA := heuristicTestMarket(tt.titleA, date)
			marketB := heuristicTestMarket(tt.titleB, date)
			result := h.Match(marketA, marketB)
			if result.IsMatch != tt.wantMatch {
				t.Errorf("IsMatch = %v, want %v (confidence: %.2f)", result.IsMatch, tt.wantMatch, result.Confidence)
			}
			if tt.wantMatch && result.Confidence < tt.wantMinConf {
				t.Errorf("Confidence = %v, want >= %v", result.Confidence, tt.wantMinConf)
			}
		})
	}
}

func TestHeuristicRejectsUnrelatedMarkets(t *testing.T) {
	h := equivalence.NewHeuristicMatcher(0.80)
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		titleA string
		titleB string
	}{
		{
			name:   "crypto vs politics",
			titleA: "will bitcoin exceed 100000 in 2026",
			titleB: "will democrats control the house 2026",
		},
		{
			name:   "sports vs economics",
			titleA: "will the chiefs win the super bowl",
			titleB: "will the fed cut interest rates in 2026",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marketA := heuristicTestMarket(tt.titleA, date)
			marketB := heuristicTestMarket(tt.titleB, date)
			result := h.Match(marketA, marketB)
			if result.IsMatch {
				t.Errorf("IsMatch = true for unrelated markets, want false (confidence: %.2f)", result.Confidence)
			}
		})
	}
}

func TestHeuristicRejectsDifferentDates(t *testing.T) {
	h := equivalence.NewHeuristicMatcher(0.80)
	// Same title but very different dates — date score should drag confidence below threshold.
	title := "will democrats control the house 2026"
	dateA := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dateB := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) // 6 months apart

	marketA := heuristicTestMarket(title, dateA)
	marketB := heuristicTestMarket(title, dateB)

	result := h.Match(marketA, marketB)

	// Entity overlap is 1.0 but date score is 0.0 (>30 days apart).
	// Combined = (1.0 * 0.60) + (0.0 * 0.40) = 0.60 < 0.80 → no match.
	if result.IsMatch {
		t.Errorf("IsMatch = true for same title but very different dates, want false (confidence: %.2f)", result.Confidence)
	}
}

func TestHeuristicConfidenceIsInRange(t *testing.T) {
	h := equivalence.NewHeuristicMatcher(0.80)
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

	pairs := [][2]string{
		{"will democrats control the house 2026", "will democrats control the house 2026"},
		{"will bitcoin exceed 100000 in 2026", "will democrats control the house 2026"},
		{"will the fed cut rates in 2026", "will the federal reserve lower rates 2026"},
	}

	for _, pair := range pairs {
		marketA := heuristicTestMarket(pair[0], date)
		marketB := heuristicTestMarket(pair[1], date)
		result := h.Match(marketA, marketB)
		if result.Confidence < 0.0 || result.Confidence > 1.0 {
			t.Errorf("Confidence = %v out of [0.0, 1.0]", result.Confidence)
		}
	}
}

func TestHeuristicHighConfidenceAboveThreshold(t *testing.T) {
	h := equivalence.NewHeuristicMatcher(0.80)
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

	// Identical title and date — should score ~1.0 and IsMatch=true.
	title := "will republicans win the senate 2026"
	marketA := heuristicTestMarket(title, date)
	marketB := heuristicTestMarket(title, date)

	result := h.Match(marketA, marketB)

	if !result.IsMatch {
		t.Errorf("IsMatch = false, want true for identical title+date")
	}
	if result.Confidence < 0.80 {
		t.Errorf("Confidence = %v, want >= 0.80 for high-confidence pair", result.Confidence)
	}
}

func TestHeuristicLowConfidenceBelowThreshold(t *testing.T) {
	h := equivalence.NewHeuristicMatcher(0.80)
	dateA := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	dateB := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

	// Different entities, same date — entity Jaccard will be low.
	marketA := heuristicTestMarket("will bitcoin exceed 100000", dateA)
	marketB := heuristicTestMarket("will democrats control house", dateB)

	result := h.Match(marketA, marketB)

	if result.IsMatch {
		t.Errorf("IsMatch = true, want false for low-entity-overlap pair")
	}
	if result.Confidence >= 0.80 {
		t.Errorf("Confidence = %v, want < 0.80 for low-confidence pair", result.Confidence)
	}
}

func TestHeuristicHandlesEmptyTitles(t *testing.T) {
	h := equivalence.NewHeuristicMatcher(0.80)
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		titleA string
		titleB string
	}{
		{name: "both empty", titleA: "", titleB: ""},
		{name: "one empty", titleA: "will democrats control house 2026", titleB: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marketA := heuristicTestMarket(tt.titleA, date)
			marketB := heuristicTestMarket(tt.titleB, date)
			// Must not panic; result fields must be well-formed.
			result := h.Match(marketA, marketB)
			if result.Confidence < 0.0 || result.Confidence > 1.0 {
				t.Errorf("Confidence %v out of range for empty title test", result.Confidence)
			}
			if result.Method != "heuristic" {
				t.Errorf("Method = %q, want 'heuristic'", result.Method)
			}
		})
	}
}
