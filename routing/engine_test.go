package routing

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/equinox/logger"
	"github.com/equinox/models"
)

// testMarket builds a fully-populated test market.
// yesPrice is the YesMid; YesBid = yesPrice-0.01, YesAsk = yesPrice+0.01.
func testMarket(venue, title string, yesPrice float64) models.Market {
	return models.Market{
		ID:         "test-id-" + venue,
		VenueID:    "native-id-" + venue,
		Venue:      venue,
		Title:      title,
		YesBid:     yesPrice - 0.01,
		YesAsk:     yesPrice + 0.01,
		YesMid:     yesPrice,
		NoPrice:    1.0 - yesPrice,
		Spread:     0.02,
		Liquidity:  100_000,
		Volume24h:  50_000,
		ResolvesAt: time.Now().Add(30 * 24 * time.Hour),
		FetchedAt:  time.Now(),
		Status:     "open",
		Category:   "politics",
	}
}

// testMatchResult wraps two markets into a MatchResult.
func testMatchResult(a, b models.Market) models.MatchResult {
	return models.MatchResult{
		MarketA:    a,
		MarketB:    b,
		IsMatch:    true,
		Confidence: 0.95,
		Method:     "heuristic",
		Reasoning:  "test match",
		MatchedAt:  time.Now(),
	}
}

// freshEngine returns an Engine with a 2-minute staleness window.
func freshEngine() *Engine {
	return NewEngine(2*time.Minute, nil)
}

// ── Weight constant test ────────────────────────────────────────────────────

func TestRoutingWeightsAddToOne(t *testing.T) {
	sum := PriceWeight + LiquidityWeight + SpreadWeight
	if sum < 0.9999 || sum > 1.0001 {
		t.Errorf("weights sum to %v, want 1.0 (PriceWeight=%.2f LiquidityWeight=%.2f SpreadWeight=%.2f)",
			sum, PriceWeight, LiquidityWeight, SpreadWeight)
	}
}

// ── ScoreVenue standalone tests ─────────────────────────────────────────────

func TestRoutingScoresInRange(t *testing.T) {
	eng := freshEngine()

	markets := []models.Market{
		testMarket("kalshi", "test market", 0.50),
		testMarket("polymarket", "test market", 0.10),
		testMarket("kalshi", "test market", 0.90),
		{
			Venue:     "kalshi",
			YesMid:    0.0,
			Spread:    0.0,
			Liquidity: 0,
			FetchedAt: time.Now(),
			Status:    "open",
		},
		{
			Venue:     "polymarket",
			YesMid:    1.0,
			YesAsk:    1.0,
			NoPrice:   0.0,
			Spread:    1.0,
			Liquidity: 10_000_000,
			FetchedAt: time.Now(),
			Status:    "open",
		},
	}

	for _, m := range markets {
		vs := eng.ScoreVenue(m)
		for _, score := range []struct {
			name string
			val  float64
		}{
			{"PriceScore", vs.PriceScore},
			{"SpreadScore", vs.SpreadScore},
			{"LiquidityScore", vs.LiquidityScore},
			{"TotalScore", vs.TotalScore},
		} {
			if score.val < 0.0 || score.val > 1.0 {
				t.Errorf("ScoreVenue(%s).%s = %v, want in [0,1]", m.Venue, score.name, score.val)
			}
		}
	}
}

// ── Route() decision tests ──────────────────────────────────────────────────

func TestRoutingPriceAdvantageWins(t *testing.T) {
	eng := freshEngine()

	// kalshi has significantly lower ask price (better for yes buyer)
	kalshi := testMarket("kalshi", "will x happen", 0.45)
	kalshi.YesAsk = 0.46

	poly := testMarket("polymarket", "will x happen", 0.55)
	poly.YesAsk = 0.56

	// Same spread and liquidity so only price differs
	kalshi.Spread = 0.02
	poly.Spread = 0.02
	kalshi.Liquidity = 100_000
	poly.Liquidity = 100_000

	decision, err := eng.Route(testMatchResult(kalshi, poly), "yes", 500)
	if err != nil {
		t.Fatalf("Route() returned unexpected error: %v", err)
	}
	if decision.RecommendedVenue != "kalshi" {
		t.Errorf("RecommendedVenue = %q, want %q (kalshi has better price)", decision.RecommendedVenue, "kalshi")
	}
}

func TestRoutingLiquidityAdvantageWins(t *testing.T) {
	eng := freshEngine()

	// Same price and spread; kalshi has 10x the liquidity
	kalshi := testMarket("kalshi", "will x happen", 0.50)
	kalshi.Liquidity = 1_000_000

	poly := testMarket("polymarket", "will x happen", 0.50)
	poly.Liquidity = 100_000

	decision, err := eng.Route(testMatchResult(kalshi, poly), "yes", 500)
	if err != nil {
		t.Fatalf("Route() returned unexpected error: %v", err)
	}
	if decision.RecommendedVenue != "kalshi" {
		t.Errorf("RecommendedVenue = %q, want %q (kalshi has deeper book)", decision.RecommendedVenue, "kalshi")
	}
}

func TestRoutingSpreadAdvantageWins(t *testing.T) {
	eng := freshEngine()

	// Same price and liquidity; kalshi has significantly tighter spread
	kalshi := testMarket("kalshi", "will x happen", 0.50)
	kalshi.Spread = 0.01
	kalshi.YesBid = 0.495
	kalshi.YesAsk = 0.505

	poly := testMarket("polymarket", "will x happen", 0.50)
	poly.Spread = 0.10
	poly.YesBid = 0.45
	poly.YesAsk = 0.55

	kalshi.Liquidity = 100_000
	poly.Liquidity = 100_000

	decision, err := eng.Route(testMatchResult(kalshi, poly), "yes", 500)
	if err != nil {
		t.Fatalf("Route() returned unexpected error: %v", err)
	}
	if decision.RecommendedVenue != "kalshi" {
		t.Errorf("RecommendedVenue = %q, want %q (kalshi has tighter spread)", decision.RecommendedVenue, "kalshi")
	}
}

func TestRoutingMixedSignalsProducesWarning(t *testing.T) {
	eng := freshEngine()

	// kalshi: better price (lower ask), worse liquidity and spread
	// polymarket: worse price, better liquidity and tighter spread
	// → conflicting signals → warning expected
	kalshi := testMarket("kalshi", "will x happen", 0.40)
	kalshi.YesAsk = 0.41
	kalshi.Spread = 0.08
	kalshi.Liquidity = 50_000

	poly := testMarket("polymarket", "will x happen", 0.60)
	poly.YesAsk = 0.61
	poly.Spread = 0.01
	poly.Liquidity = 500_000

	decision, err := eng.Route(testMatchResult(kalshi, poly), "yes", 500)
	if err != nil {
		t.Fatalf("Route() returned unexpected error: %v", err)
	}

	hasMixedWarning := false
	for _, w := range decision.Warnings {
		if strings.Contains(strings.ToLower(w), "mixed") {
			hasMixedWarning = true
			break
		}
	}
	if !hasMixedWarning {
		t.Errorf("expected a mixed-signals warning in Warnings, got: %v", decision.Warnings)
	}
}

func TestRoutingHandlesUnavailableVenue(t *testing.T) {
	eng := freshEngine()

	kalshi := testMarket("kalshi", "will x happen", 0.50)
	poly := testMarket("polymarket", "will x happen", 0.50)
	poly.Status = "closed" // polymarket is unavailable

	decision, err := eng.Route(testMatchResult(kalshi, poly), "yes", 500)
	if err != nil {
		t.Fatalf("Route() returned unexpected error: %v", err)
	}
	if decision.RecommendedVenue != "kalshi" {
		t.Errorf("RecommendedVenue = %q, want %q (polymarket unavailable)", decision.RecommendedVenue, "kalshi")
	}

	hasUnavailableWarning := false
	for _, w := range decision.Warnings {
		if strings.Contains(strings.ToLower(w), "unavailable") {
			hasUnavailableWarning = true
			break
		}
	}
	if !hasUnavailableWarning {
		t.Errorf("expected an unavailable-venue warning in Warnings, got: %v", decision.Warnings)
	}
}

func TestRoutingStaleDataProducesWarning(t *testing.T) {
	eng := freshEngine() // staleness threshold: 2 minutes

	kalshi := testMarket("kalshi", "will x happen", 0.50)
	kalshi.FetchedAt = time.Now().Add(-5 * time.Minute) // 5 minutes old → stale

	poly := testMarket("polymarket", "will x happen", 0.50)

	decision, err := eng.Route(testMatchResult(kalshi, poly), "yes", 500)
	if err != nil {
		t.Fatalf("Route() returned unexpected error: %v", err)
	}

	hasStaleWarning := false
	for _, w := range decision.Warnings {
		if strings.Contains(strings.ToLower(w), "stale") {
			hasStaleWarning = true
			break
		}
	}
	if !hasStaleWarning {
		t.Errorf("expected a stale-data warning in Warnings, got: %v", decision.Warnings)
	}
}

func TestRoutingDecisionHasReasoning(t *testing.T) {
	tests := []struct {
		name    string
		marketA models.Market
		marketB models.Market
		side    string
	}{
		{
			name:    "clear price winner",
			marketA: func() models.Market { m := testMarket("kalshi", "test", 0.40); return m }(),
			marketB: func() models.Market { m := testMarket("polymarket", "test", 0.60); return m }(),
			side:    "yes",
		},
		{
			name:    "clear liquidity winner",
			marketA: func() models.Market { m := testMarket("kalshi", "test", 0.50); m.Liquidity = 1_000_000; return m }(),
			marketB: func() models.Market { m := testMarket("polymarket", "test", 0.50); m.Liquidity = 100_000; return m }(),
			side:    "no",
		},
		{
			name:    "unavailable venue",
			marketA: func() models.Market { m := testMarket("kalshi", "test", 0.50); return m }(),
			marketB: func() models.Market { m := testMarket("polymarket", "test", 0.50); m.Status = "resolved"; return m }(),
			side:    "yes",
		},
	}

	eng := freshEngine()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := eng.Route(testMatchResult(tt.marketA, tt.marketB), tt.side, 100)
			if err != nil {
				t.Fatalf("Route() error: %v", err)
			}
			if decision.Reasoning == "" {
				t.Error("Reasoning is empty — must always be populated")
			}
		})
	}
}

func TestRoutingDecisionHasVenue(t *testing.T) {
	tests := []struct {
		name    string
		marketA models.Market
		marketB models.Market
	}{
		{
			name:    "both available",
			marketA: testMarket("kalshi", "test", 0.50),
			marketB: testMarket("polymarket", "test", 0.55),
		},
		{
			name:    "only kalshi available",
			marketA: testMarket("kalshi", "test", 0.50),
			marketB: func() models.Market { m := testMarket("polymarket", "test", 0.55); m.Status = "closed"; return m }(),
		},
		{
			name:    "only polymarket available",
			marketA: func() models.Market { m := testMarket("kalshi", "test", 0.50); m.Status = "closed"; return m }(),
			marketB: testMarket("polymarket", "test", 0.55),
		},
	}

	eng := freshEngine()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := eng.Route(testMatchResult(tt.marketA, tt.marketB), "yes", 100)
			if err != nil {
				t.Fatalf("Route() error: %v", err)
			}
			if decision.RecommendedVenue == "" {
				t.Error("RecommendedVenue is empty — must always be populated")
			}
		})
	}
}

func TestRoutingTieBreaksToHigherLiquidity(t *testing.T) {
	eng := freshEngine()

	// kalshi: worse price (higher YesAsk), but more liquidity
	// polymarket: better price (lower YesAsk), less liquidity
	//
	// With weights Price=40%, Liq=35%, Spread=25% and equal spreads:
	//   kalshi total  ≈ 0.0*0.40 + 1.0*0.35 + 0.5*0.25 = 0.475
	//   poly   total  ≈ 1.0*0.40 + 0.0*0.35 + 0.5*0.25 = 0.525
	// Difference = 0.05 → exactly at the tie threshold → liquidity tiebreak.
	// kalshi has higher raw liquidity → kalshi wins the tiebreak.
	kalshi := testMarket("kalshi", "will x happen", 0.51)
	kalshi.YesAsk = 0.52
	kalshi.Spread = 0.02
	kalshi.Liquidity = 200_000

	poly := testMarket("polymarket", "will x happen", 0.47)
	poly.YesAsk = 0.48
	poly.Spread = 0.02
	poly.Liquidity = 100_000

	decision, err := eng.Route(testMatchResult(kalshi, poly), "yes", 500)
	if err != nil {
		t.Fatalf("Route() returned unexpected error: %v", err)
	}
	if decision.RecommendedVenue != "kalshi" {
		t.Errorf("RecommendedVenue = %q, want %q (tie → prefer higher liquidity = kalshi)", decision.RecommendedVenue, "kalshi")
	}

	hasTieWarning := false
	for _, w := range decision.Warnings {
		if strings.Contains(strings.ToLower(w), "tie") {
			hasTieWarning = true
			break
		}
	}
	if !hasTieWarning {
		t.Errorf("expected a tie-break warning in Warnings, got: %v", decision.Warnings)
	}
}

// TestRoutingBothVenuesUnavailable verifies that Route returns an error when
// neither venue is open — no RoutingDecision can be produced.
func TestRoutingBothVenuesUnavailable(t *testing.T) {
	eng := freshEngine()

	kalshi := testMarket("kalshi", "will x happen", 0.50)
	kalshi.Status = "closed"

	poly := testMarket("polymarket", "will x happen", 0.50)
	poly.Status = "resolved"

	_, err := eng.Route(testMatchResult(kalshi, poly), "yes", 500)
	if err == nil {
		t.Error("Route() returned nil error when both venues are unavailable — expected an error")
	}
}

// TestRoutingNoSideRouting exercises the "no" order side, covering the NoPrice
// branch of effectivePrice and the full Route() path with side="no".
func TestRoutingNoSideRouting(t *testing.T) {
	eng := freshEngine()

	// For a "no" buyer, lower NoPrice is better.
	// kalshi NoPrice = 1 - 0.40 = 0.60 (cheaper no)
	// poly   NoPrice = 1 - 0.60 = 0.40 (cheaper no) — poly wins for "no" buyers
	kalshi := testMarket("kalshi", "will x happen", 0.40)
	poly := testMarket("polymarket", "will x happen", 0.60)
	kalshi.Liquidity = 100_000
	poly.Liquidity = 100_000

	decision, err := eng.Route(testMatchResult(kalshi, poly), "no", 200)
	if err != nil {
		t.Fatalf("Route() returned unexpected error: %v", err)
	}
	if decision.RecommendedVenue == "" {
		t.Error("RecommendedVenue is empty for 'no' side routing")
	}
	if decision.Reasoning == "" {
		t.Error("Reasoning is empty for 'no' side routing")
	}
	// poly has lower NoPrice (0.40 < 0.60) → better for "no" buyer → poly should win
	if decision.RecommendedVenue != "polymarket" {
		t.Errorf("RecommendedVenue = %q, want %q (polymarket has lower NoPrice)", decision.RecommendedVenue, "polymarket")
	}
}

// TestRoutingDefaultSideRouting covers the default case in effectivePrice when
// the caller passes an unrecognised side string.
func TestRoutingDefaultSideRouting(t *testing.T) {
	eng := freshEngine()

	kalshi := testMarket("kalshi", "will x happen", 0.40)
	poly := testMarket("polymarket", "will x happen", 0.60)

	// An unrecognised side falls back to YesMid — should still produce a decision.
	decision, err := eng.Route(testMatchResult(kalshi, poly), "maybe", 100)
	if err != nil {
		t.Fatalf("Route() returned unexpected error for unrecognised side: %v", err)
	}
	if decision.RecommendedVenue == "" {
		t.Error("RecommendedVenue is empty for default side")
	}
}

// TestRoutingWithLogger verifies that the Engine works correctly when a real
// logger is provided (covers log-guarded branches in Route and forcedDecision).
func TestRoutingWithLogger(t *testing.T) {
	log := logger.New(io.Discard) // suppress output in tests
	eng := NewEngine(2*time.Minute, log)

	// Normal two-venue route — exercises log.Info in Route.
	kalshi := testMarket("kalshi", "will x happen", 0.45)
	poly := testMarket("polymarket", "will x happen", 0.55)

	decision, err := eng.Route(testMatchResult(kalshi, poly), "yes", 500)
	if err != nil {
		t.Fatalf("Route() with logger returned unexpected error: %v", err)
	}
	if decision.RecommendedVenue == "" {
		t.Error("RecommendedVenue is empty")
	}

	// Forced-routing path — exercises log.Warn in forcedDecision.
	poly.Status = "closed"
	decision, err = eng.Route(testMatchResult(kalshi, poly), "yes", 500)
	if err != nil {
		t.Fatalf("Route() forced path with logger returned unexpected error: %v", err)
	}
	if decision.RecommendedVenue != "kalshi" {
		t.Errorf("RecommendedVenue = %q, want %q", decision.RecommendedVenue, "kalshi")
	}

	// Stale-data path — exercises log.Warn in Route staleness check.
	kalshi2 := testMarket("kalshi", "will x happen", 0.50)
	kalshi2.FetchedAt = time.Now().Add(-10 * time.Minute)
	poly2 := testMarket("polymarket", "will x happen", 0.50)
	poly2.Status = "open"
	_, err = eng.Route(testMatchResult(kalshi2, poly2), "yes", 500)
	if err != nil {
		t.Fatalf("Route() stale-data with logger returned unexpected error: %v", err)
	}

	// Mixed-signals path — exercises log.Warn for mixed signals with a logger.
	kalshi3 := testMarket("kalshi", "will x happen", 0.40)
	kalshi3.YesAsk = 0.41
	kalshi3.Spread = 0.08
	kalshi3.Liquidity = 50_000
	poly3 := testMarket("polymarket", "will x happen", 0.60)
	poly3.YesAsk = 0.61
	poly3.Spread = 0.01
	poly3.Liquidity = 500_000
	_, err = eng.Route(testMatchResult(kalshi3, poly3), "yes", 500)
	if err != nil {
		t.Fatalf("Route() mixed-signals with logger returned unexpected error: %v", err)
	}

	// Tie-break path with logger — exercises the tie-break log.Warn path.
	kalshi4 := testMarket("kalshi", "will x happen", 0.51)
	kalshi4.YesAsk = 0.52
	kalshi4.Spread = 0.02
	kalshi4.Liquidity = 200_000
	poly4 := testMarket("polymarket", "will x happen", 0.47)
	poly4.YesAsk = 0.48
	poly4.Spread = 0.02
	poly4.Liquidity = 100_000
	_, err = eng.Route(testMatchResult(kalshi4, poly4), "yes", 500)
	if err != nil {
		t.Fatalf("Route() tie-break with logger returned unexpected error: %v", err)
	}
}
