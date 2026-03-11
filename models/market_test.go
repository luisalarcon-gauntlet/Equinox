package models_test

import (
	"testing"
	"time"

	"github.com/equinox/models"
)

// ---------------------------------------------------------------------------
// Market struct field coverage
// ---------------------------------------------------------------------------

func TestMarketHasAllRequiredFields(t *testing.T) {
	now := time.Now()
	m := models.Market{
		ID:         "uuid-1234",
		VenueID:    "KXINX-23",
		Venue:      "kalshi",
		Title:      "will democrats win the house in 2026",
		YesBid:     0.44,
		YesAsk:     0.46,
		YesMid:     0.45,
		NoPrice:    0.55,
		Spread:     0.02,
		Liquidity:  250000.00,
		Volume24h:  50000.00,
		ResolvesAt: now.Add(30 * 24 * time.Hour),
		FetchedAt:  now,
		Category:   "politics",
		Status:     "open",
		RawData:    map[string]string{"raw": "data"},
	}

	if m.ID != "uuid-1234" {
		t.Errorf("ID = %q, want %q", m.ID, "uuid-1234")
	}
	if m.VenueID != "KXINX-23" {
		t.Errorf("VenueID = %q, want %q", m.VenueID, "KXINX-23")
	}
	if m.Venue != "kalshi" {
		t.Errorf("Venue = %q, want %q", m.Venue, "kalshi")
	}
	if m.YesBid != 0.44 {
		t.Errorf("YesBid = %v, want 0.44", m.YesBid)
	}
	if m.YesAsk != 0.46 {
		t.Errorf("YesAsk = %v, want 0.46", m.YesAsk)
	}
	if m.YesMid != 0.45 {
		t.Errorf("YesMid = %v, want 0.45", m.YesMid)
	}
	if m.NoPrice != 0.55 {
		t.Errorf("NoPrice = %v, want 0.55", m.NoPrice)
	}
	if m.Spread != 0.02 {
		t.Errorf("Spread = %v, want 0.02", m.Spread)
	}
	if m.Liquidity != 250000.00 {
		t.Errorf("Liquidity = %v, want 250000", m.Liquidity)
	}
	if m.Volume24h != 50000.00 {
		t.Errorf("Volume24h = %v, want 50000", m.Volume24h)
	}
	if m.Category != "politics" {
		t.Errorf("Category = %q, want %q", m.Category, "politics")
	}
	if m.Status != "open" {
		t.Errorf("Status = %q, want %q", m.Status, "open")
	}
	if m.RawData == nil {
		t.Error("RawData = nil, want populated")
	}
}

func TestMarketZeroValueIsValid(t *testing.T) {
	var m models.Market
	if m.Venue != "" {
		t.Errorf("zero-value Venue = %q, want empty string", m.Venue)
	}
	if m.YesMid != 0 {
		t.Errorf("zero-value YesMid = %v, want 0", m.YesMid)
	}
}

func TestMarketPricingInvariants(t *testing.T) {
	tests := []struct {
		name    string
		yesBid  float64
		yesAsk  float64
		yesMid  float64
		noPrice float64
		spread  float64
	}{
		{
			name:    "typical market 45 cents",
			yesBid:  0.44,
			yesAsk:  0.46,
			yesMid:  0.45,
			noPrice: 0.55,
			spread:  0.02,
		},
		{
			name:    "near-certain yes market",
			yesBid:  0.95,
			yesAsk:  0.97,
			yesMid:  0.96,
			noPrice: 0.04,
			spread:  0.02,
		},
		{
			name:    "coin-flip market",
			yesBid:  0.49,
			yesAsk:  0.51,
			yesMid:  0.50,
			noPrice: 0.50,
			spread:  0.02,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := models.Market{
				YesBid:  tt.yesBid,
				YesAsk:  tt.yesAsk,
				YesMid:  tt.yesMid,
				NoPrice: tt.noPrice,
				Spread:  tt.spread,
			}
			// Verify the invariants hold on assignment.
			wantMid := (m.YesBid + m.YesAsk) / 2
			if abs(m.YesMid-wantMid) > 1e-9 {
				t.Errorf("YesMid %v != (YesBid+YesAsk)/2 = %v", m.YesMid, wantMid)
			}
			wantSpread := m.YesAsk - m.YesBid
			if abs(m.Spread-wantSpread) > 1e-9 {
				t.Errorf("Spread %v != YesAsk-YesBid = %v", m.Spread, wantSpread)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MatchResult struct field coverage
// ---------------------------------------------------------------------------

func TestMatchResultHasAllRequiredFields(t *testing.T) {
	now := time.Now()
	a := models.Market{Venue: "kalshi", Title: "will bitcoin hit 100k"}
	b := models.Market{Venue: "polymarket", Title: "will btc reach 100000"}

	mr := models.MatchResult{
		MarketA:    a,
		MarketB:    b,
		IsMatch:    true,
		Confidence: 0.92,
		Method:     "heuristic",
		Reasoning:  "identical entities and date",
		Warnings:   []string{"stale data on kalshi"},
		MatchedAt:  now,
	}

	if mr.MarketA.Venue != "kalshi" {
		t.Errorf("MarketA.Venue = %q", mr.MarketA.Venue)
	}
	if mr.MarketB.Venue != "polymarket" {
		t.Errorf("MarketB.Venue = %q", mr.MarketB.Venue)
	}
	if !mr.IsMatch {
		t.Error("IsMatch = false, want true")
	}
	if mr.Confidence != 0.92 {
		t.Errorf("Confidence = %v, want 0.92", mr.Confidence)
	}
	if mr.Method != "heuristic" {
		t.Errorf("Method = %q, want heuristic", mr.Method)
	}
	if len(mr.Warnings) != 1 {
		t.Errorf("Warnings len = %d, want 1", len(mr.Warnings))
	}
	if mr.MatchedAt.IsZero() {
		t.Error("MatchedAt is zero")
	}
}

func TestMatchResultTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		isMatch    bool
		confidence float64
		method     string
	}{
		{"heuristic match", true, 0.91, "heuristic"},
		{"ai match", true, 0.85, "ai"},
		{"heuristic+ai match", true, 0.78, "heuristic+ai"},
		{"no match", false, 0.12, "heuristic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mr := models.MatchResult{
				IsMatch:    tt.isMatch,
				Confidence: tt.confidence,
				Method:     tt.method,
			}
			if mr.IsMatch != tt.isMatch {
				t.Errorf("IsMatch = %v, want %v", mr.IsMatch, tt.isMatch)
			}
			if mr.Confidence != tt.confidence {
				t.Errorf("Confidence = %v, want %v", mr.Confidence, tt.confidence)
			}
			if mr.Method != tt.method {
				t.Errorf("Method = %q, want %q", mr.Method, tt.method)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// VenueScore struct field coverage
// ---------------------------------------------------------------------------

func TestVenueScoreHasAllRequiredFields(t *testing.T) {
	vs := models.VenueScore{
		Venue:          "kalshi",
		PriceScore:     0.80,
		SpreadScore:    0.75,
		LiquidityScore: 0.90,
		TotalScore:     0.83,
	}

	if vs.Venue != "kalshi" {
		t.Errorf("Venue = %q, want kalshi", vs.Venue)
	}
	if vs.PriceScore != 0.80 {
		t.Errorf("PriceScore = %v, want 0.80", vs.PriceScore)
	}
	if vs.SpreadScore != 0.75 {
		t.Errorf("SpreadScore = %v, want 0.75", vs.SpreadScore)
	}
	if vs.LiquidityScore != 0.90 {
		t.Errorf("LiquidityScore = %v, want 0.90", vs.LiquidityScore)
	}
	if vs.TotalScore != 0.83 {
		t.Errorf("TotalScore = %v, want 0.83", vs.TotalScore)
	}
}

func TestVenueScoreTableDriven(t *testing.T) {
	tests := []struct {
		name           string
		venue          string
		priceScore     float64
		spreadScore    float64
		liquidityScore float64
		totalScore     float64
	}{
		{"kalshi high scores", "kalshi", 0.90, 0.85, 0.95, 0.90},
		{"polymarket low scores", "polymarket", 0.30, 0.40, 0.20, 0.30},
		{"zero scores", "", 0, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := models.VenueScore{
				Venue:          tt.venue,
				PriceScore:     tt.priceScore,
				SpreadScore:    tt.spreadScore,
				LiquidityScore: tt.liquidityScore,
				TotalScore:     tt.totalScore,
			}
			if vs.PriceScore != tt.priceScore {
				t.Errorf("PriceScore = %v, want %v", vs.PriceScore, tt.priceScore)
			}
			if vs.TotalScore != tt.totalScore {
				t.Errorf("TotalScore = %v, want %v", vs.TotalScore, tt.totalScore)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// RoutingDecision struct field coverage
// ---------------------------------------------------------------------------

func TestRoutingDecisionHasAllRequiredFields(t *testing.T) {
	now := time.Now()
	m := models.Market{Venue: "kalshi", Title: "will the fed cut rates in 2026"}

	rd := models.RoutingDecision{
		Market:           m,
		OrderSide:        "yes",
		OrderSize:        500.00,
		RecommendedVenue: "kalshi",
		Confidence:       0.87,
		VenueScores: []models.VenueScore{
			{Venue: "kalshi", TotalScore: 0.87},
			{Venue: "polymarket", TotalScore: 0.72},
		},
		Reasoning: "Kalshi has 5x the liquidity and a tighter spread",
		Warnings:  []string{"polymarket data is 3 minutes old"},
		DecidedAt: now,
	}

	if rd.OrderSide != "yes" {
		t.Errorf("OrderSide = %q, want yes", rd.OrderSide)
	}
	if rd.OrderSize != 500.00 {
		t.Errorf("OrderSize = %v, want 500", rd.OrderSize)
	}
	if rd.RecommendedVenue != "kalshi" {
		t.Errorf("RecommendedVenue = %q, want kalshi", rd.RecommendedVenue)
	}
	if rd.Confidence != 0.87 {
		t.Errorf("Confidence = %v, want 0.87", rd.Confidence)
	}
	if len(rd.VenueScores) != 2 {
		t.Errorf("VenueScores len = %d, want 2", len(rd.VenueScores))
	}
	if rd.Reasoning == "" {
		t.Error("Reasoning is empty")
	}
	if len(rd.Warnings) != 1 {
		t.Errorf("Warnings len = %d, want 1", len(rd.Warnings))
	}
	if rd.DecidedAt.IsZero() {
		t.Error("DecidedAt is zero")
	}
}

func TestRoutingDecisionTableDriven(t *testing.T) {
	tests := []struct {
		name             string
		side             string
		size             float64
		recommendedVenue string
		confidence       float64
		reasoning        string
	}{
		{"yes side kalshi", "yes", 500, "kalshi", 0.87, "better price"},
		{"no side polymarket", "no", 1000, "polymarket", 0.65, "higher liquidity"},
		{"small order kalshi", "yes", 50, "kalshi", 0.91, "tighter spread"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rd := models.RoutingDecision{
				OrderSide:        tt.side,
				OrderSize:        tt.size,
				RecommendedVenue: tt.recommendedVenue,
				Confidence:       tt.confidence,
				Reasoning:        tt.reasoning,
			}
			if rd.OrderSide != tt.side {
				t.Errorf("OrderSide = %q, want %q", rd.OrderSide, tt.side)
			}
			if rd.RecommendedVenue != tt.recommendedVenue {
				t.Errorf("RecommendedVenue = %q, want %q", rd.RecommendedVenue, tt.recommendedVenue)
			}
			if rd.Reasoning == "" {
				t.Error("Reasoning is empty")
			}
		})
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
