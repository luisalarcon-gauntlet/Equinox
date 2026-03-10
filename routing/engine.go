// Package routing implements the scoring and decision logic that recommends
// which venue to use for a hypothetical order on a matched market pair.
//
// Layer contract: routing only consumes models.MatchResult and models.Market.
// It never imports venues/ packages and never calls external APIs.
package routing

import (
	"fmt"
	"math"
	"strings"
	"time"

	equinoxerrors "github.com/equinox/errors"
	"github.com/equinox/logger"
	"github.com/equinox/models"
)

// Scoring weights — justified design decisions documented in docs/ROUTING.md.
// Invariant: PriceWeight + LiquidityWeight + SpreadWeight == 1.0
const (
	// PriceWeight is the highest weight because it directly affects P&L.
	PriceWeight = 0.40

	// LiquidityWeight is second because order fill depends on book depth.
	LiquidityWeight = 0.35

	// SpreadWeight is third; a tight spread signals a healthy, competitive market.
	SpreadWeight = 0.25

	// tieThreshold is the maximum TotalScore difference treated as a tie.
	// Scores within 5% of each other trigger a liquidity-based tiebreak.
	tieThreshold = 0.05

	// tieEpsilon guards against IEEE-754 drift when the computed diff lands
	// infinitesimally above 0.05 due to floating-point rounding in the weight
	// multiplications. 1e-9 is far smaller than any meaningful score difference.
	tieEpsilon = 1e-9

	// liquidityReference is the $-value used to normalise single-venue
	// liquidity scores on a logarithmic scale in ScoreVenue.
	liquidityReference = 1_000_000.0
)

// Engine scores venues and produces routing decisions.
// It holds no mutable state; all inputs are passed per call.
type Engine struct {
	stalenessThreshold time.Duration
	log                *logger.Logger
}

// NewEngine constructs an Engine.
//
//   - stalenessThreshold: how old FetchedAt may be before a warning is added.
//   - log: optional structured logger (nil = no logging).
func NewEngine(stalenessThreshold time.Duration, log *logger.Logger) *Engine {
	return &Engine{
		stalenessThreshold: stalenessThreshold,
		log:                log,
	}
}

// ScoreVenue computes absolute (single-venue, side-agnostic) sub-scores for
// a market. All returned scores are in [0, 1] — higher is always better.
//
// These scores are useful for inspection and are also used as the basis for
// the forced-routing path (when only one venue is available). The Route()
// method uses cross-venue min-max normalisation for head-to-head comparisons.
//
//   - PriceScore:     1 − YesMid  (lower mid-price → cheaper yes position)
//   - SpreadScore:    clamp(1 − Spread, 0, 1)  (tighter spread → higher score)
//   - LiquidityScore: log-scale against $1 M reference
func (e *Engine) ScoreVenue(market models.Market) models.VenueScore {
	priceScore := math.Max(0, math.Min(1, 1.0-market.YesMid))
	spreadScore := math.Max(0, math.Min(1, 1.0-market.Spread))
	liqScore := math.Min(1.0, math.Log1p(market.Liquidity)/math.Log1p(liquidityReference))

	total := priceScore*PriceWeight + liqScore*LiquidityWeight + spreadScore*SpreadWeight

	return models.VenueScore{
		Venue:          market.Venue,
		PriceScore:     priceScore,
		SpreadScore:    spreadScore,
		LiquidityScore: liqScore,
		TotalScore:     total,
	}
}

// Route produces a RoutingDecision for a hypothetical order on a matched pair.
//
// Decision flow (mirrors docs/ROUTING.md):
//  1. Staleness check — warn if FetchedAt is older than stalenessThreshold.
//  2. Availability check — if one venue is closed/resolved, route to the other.
//  3. Cross-venue min-max normalisation for Price, Spread, and Liquidity.
//  4. Mixed-signals detection — warn when metrics disagree.
//  5. Determine winner; if scores are within tieThreshold, prefer higher liquidity.
//  6. Build human-readable Reasoning.
//
// Returns an EquinoxError only when both venues are unavailable.
func (e *Engine) Route(match models.MatchResult, side string, size float64) (models.RoutingDecision, error) {
	var warnings []string
	now := time.Now()

	marketA := match.MarketA
	marketB := match.MarketB

	// ── Step 1: Staleness check ───────────────────────────────────────────
	if e.isStale(marketA, now) {
		age := now.Sub(marketA.FetchedAt).Round(time.Second)
		msg := fmt.Sprintf(
			"stale data: %s price data is %s old (threshold %s) — scores may not reflect current market",
			marketA.Venue, age, e.stalenessThreshold,
		)
		warnings = append(warnings, msg)
		if e.log != nil {
			e.log.Warn("routing", marketA.Venue, msg)
		}
	}
	if e.isStale(marketB, now) {
		age := now.Sub(marketB.FetchedAt).Round(time.Second)
		msg := fmt.Sprintf(
			"stale data: %s price data is %s old (threshold %s) — scores may not reflect current market",
			marketB.Venue, age, e.stalenessThreshold,
		)
		warnings = append(warnings, msg)
		if e.log != nil {
			e.log.Warn("routing", marketB.Venue, msg)
		}
	}

	// ── Step 2: Availability check ────────────────────────────────────────
	aAvailable := marketA.Status == "open"
	bAvailable := marketB.Status == "open"

	if !aAvailable && !bAvailable {
		return models.RoutingDecision{}, &equinoxerrors.EquinoxError{
			Layer:   "routing",
			Message: fmt.Sprintf("both venues unavailable (%s status=%q, %s status=%q)", marketA.Venue, marketA.Status, marketB.Venue, marketB.Status),
		}
	}

	if !aAvailable {
		return e.forcedDecision(marketA, marketB, match, side, size, warnings, now), nil
	}
	if !bAvailable {
		return e.forcedDecision(marketB, marketA, match, side, size, warnings, now), nil
	}

	// ── Step 3: Cross-venue scoring ───────────────────────────────────────
	scoreA, scoreB := e.scoreVenuePair(marketA, marketB, side)

	// ── Step 4: Mixed-signals detection ──────────────────────────────────
	if isMixedSignals(scoreA, scoreB) {
		msg := "mixed signals: price, liquidity, and spread metrics do not all favour the same venue — review scores before routing"
		warnings = append(warnings, msg)
		if e.log != nil {
			e.log.Warn("routing", "", msg)
		}
	}

	// ── Step 5: Decision ──────────────────────────────────────────────────
	diff := math.Abs(scoreA.TotalScore - scoreB.TotalScore)
	isTie := diff <= tieThreshold+tieEpsilon

	var recommended models.Market
	if isTie {
		warnings = append(warnings, fmt.Sprintf(
			"scores within %.0f%% (%.3f vs %.3f) — tie-break applied: routing to venue with higher liquidity",
			tieThreshold*100, scoreA.TotalScore, scoreB.TotalScore,
		))
		if marketA.Liquidity >= marketB.Liquidity {
			recommended = marketA
		} else {
			recommended = marketB
		}
	} else if scoreA.TotalScore > scoreB.TotalScore {
		recommended = marketA
	} else {
		recommended = marketB
	}

	// Confidence is the score margin clamped to [0, 1].
	confidence := math.Min(1.0, diff)

	reasoning := e.buildReasoning(recommended, marketA, marketB, scoreA, scoreB, side, size, isTie)

	if e.log != nil {
		e.log.Info("routing", "", fmt.Sprintf(
			"route $%.0f %s → %s (confidence %.2f | %s=%.3f %s=%.3f)",
			size, strings.ToUpper(side), recommended.Venue, confidence,
			marketA.Venue, scoreA.TotalScore, marketB.Venue, scoreB.TotalScore,
		))
	}

	return models.RoutingDecision{
		Market:           recommended,
		OrderSide:        side,
		OrderSize:        size,
		RecommendedVenue: recommended.Venue,
		Confidence:       confidence,
		VenueScores:      []models.VenueScore{scoreA, scoreB},
		Reasoning:        reasoning,
		Warnings:         warnings,
		DecidedAt:        now,
	}, nil
}

// ── Internal helpers ─────────────────────────────────────────────────────────

// isStale reports whether the market's price data is older than the threshold.
func (e *Engine) isStale(market models.Market, now time.Time) bool {
	return !market.FetchedAt.IsZero() && now.Sub(market.FetchedAt) > e.stalenessThreshold
}

// scoreVenuePair computes cross-venue min-max normalised VenueScores for both
// markets simultaneously. When two venues have the same value for a metric,
// both receive 0.5 (tied). When they differ, the better venue gets 1.0 and
// the worse venue gets 0.0.
func (e *Engine) scoreVenuePair(a, b models.Market, side string) (models.VenueScore, models.VenueScore) {
	// Price: lower effective cost → better → invert normalization.
	priceA := effectivePrice(a, side)
	priceB := effectivePrice(b, side)
	psA, psB := normalizeInverted(priceA, priceB)

	// Spread: lower spread → better → invert normalization.
	ssA, ssB := normalizeInverted(a.Spread, b.Spread)

	// Liquidity: higher → better → direct normalization.
	lsA, lsB := normalizeDirect(a.Liquidity, b.Liquidity)

	totalA := psA*PriceWeight + lsA*LiquidityWeight + ssA*SpreadWeight
	totalB := psB*PriceWeight + lsB*LiquidityWeight + ssB*SpreadWeight

	return models.VenueScore{
			Venue: a.Venue, PriceScore: psA, SpreadScore: ssA, LiquidityScore: lsA, TotalScore: totalA,
		}, models.VenueScore{
			Venue: b.Venue, PriceScore: psB, SpreadScore: ssB, LiquidityScore: lsB, TotalScore: totalB,
		}
}

// effectivePrice returns the cost of acquiring a unit position for the given
// order side:
//   - "yes": YesAsk — the price a yes buyer pays.
//   - "no":  NoPrice (= 1 − YesMid) — the cost of a no position.
//   - other: YesMid — neutral midpoint.
func effectivePrice(market models.Market, side string) float64 {
	switch strings.ToLower(side) {
	case "yes":
		return market.YesAsk
	case "no":
		return market.NoPrice
	default:
		return market.YesMid
	}
}

// normalizeDirect maps (a, b) to [0,1] where higher raw value → higher score.
// Ties → both return 0.5.
func normalizeDirect(a, b float64) (float64, float64) {
	if a == b {
		return 0.5, 0.5
	}
	lo, hi := a, b
	if b < a {
		lo, hi = b, a
	}
	span := hi - lo
	return (a - lo) / span, (b - lo) / span
}

// normalizeInverted maps (a, b) to [0,1] where lower raw value → higher score.
func normalizeInverted(a, b float64) (float64, float64) {
	sa, sb := normalizeDirect(a, b)
	return 1 - sa, 1 - sb
}

// isMixedSignals returns true when at least two of the three sub-metrics
// favour different venues, indicating conflicting market quality signals.
func isMixedSignals(a, b models.VenueScore) bool {
	// Collect which venue "wins" each non-tied metric.
	type vote bool // true = venue A wins
	var votes []vote

	if a.PriceScore != b.PriceScore {
		votes = append(votes, a.PriceScore > b.PriceScore)
	}
	if a.LiquidityScore != b.LiquidityScore {
		votes = append(votes, a.LiquidityScore > b.LiquidityScore)
	}
	if a.SpreadScore != b.SpreadScore {
		votes = append(votes, a.SpreadScore > b.SpreadScore)
	}

	if len(votes) < 2 {
		return false // not enough distinct signals to detect a conflict
	}

	first := votes[0]
	for _, v := range votes[1:] {
		if v != first {
			return true
		}
	}
	return false
}

// forcedDecision builds a RoutingDecision when only one venue is available.
//
//   - unavailable: the market that cannot be used.
//   - available:   the market we must route to.
func (e *Engine) forcedDecision(
	unavailable, available models.Market,
	match models.MatchResult,
	side string,
	size float64,
	warnings []string,
	now time.Time,
) models.RoutingDecision {
	w := fmt.Sprintf(
		"venue unavailable: %s is not available (status: %q) — routing exclusively to %s",
		unavailable.Venue, unavailable.Status, available.Venue,
	)
	warnings = append(warnings, w)
	if e.log != nil {
		e.log.Warn("routing", unavailable.Venue, w)
	}

	availScore := e.ScoreVenue(available)
	var zeroScore models.VenueScore
	zeroScore.Venue = unavailable.Venue

	// Preserve MarketA / MarketB order in VenueScores for consistent API output.
	var venueScores []models.VenueScore
	if match.MarketA.Venue == available.Venue {
		venueScores = []models.VenueScore{availScore, zeroScore}
	} else {
		venueScores = []models.VenueScore{zeroScore, availScore}
	}

	reasoning := fmt.Sprintf(
		"routed exclusively to %s because %s is unavailable (status: %q); "+
			"%s effective price: %.4f, spread: %.4f, liquidity: $%.0f",
		available.Venue, unavailable.Venue, unavailable.Status,
		available.Venue, effectivePrice(available, side), available.Spread, available.Liquidity,
	)

	return models.RoutingDecision{
		Market:           available,
		OrderSide:        side,
		OrderSize:        size,
		RecommendedVenue: available.Venue,
		Confidence:       1.0, // only one choice — maximum certainty
		VenueScores:      venueScores,
		Reasoning:        reasoning,
		Warnings:         warnings,
		DecidedAt:        now,
	}
}

// buildReasoning constructs a human-readable explanation of the routing decision.
func (e *Engine) buildReasoning(
	recommended, a, b models.Market,
	scoreA, scoreB models.VenueScore,
	side string,
	size float64,
	isTie bool,
) string {
	winner, loser := scoreA, scoreB
	winnerMkt, loserMkt := a, b
	if recommended.Venue == b.Venue {
		winner, loser = scoreB, scoreA
		winnerMkt, loserMkt = b, a
	}

	var parts []string

	verb := "wins"
	if isTie {
		verb = "wins (tie-break)"
	}
	parts = append(parts, fmt.Sprintf(
		"%s %s for $%.0f %s order (score %.3f vs %s %.3f)",
		winner.Venue, verb, size, strings.ToUpper(side),
		winner.TotalScore, loser.Venue, loser.TotalScore,
	))

	if isTie {
		parts = append(parts, "scores within 5% — tie resolved by higher liquidity depth")
	}

	// Explain which metrics drove the decision.
	var advantages []string
	if winner.PriceScore > loser.PriceScore {
		advantages = append(advantages, fmt.Sprintf(
			"better %s price (%.4f vs %.4f)",
			strings.ToUpper(side),
			effectivePrice(winnerMkt, side),
			effectivePrice(loserMkt, side),
		))
	}
	if winner.LiquidityScore > loser.LiquidityScore {
		advantages = append(advantages, fmt.Sprintf(
			"deeper liquidity ($%.0f vs $%.0f)",
			winnerMkt.Liquidity, loserMkt.Liquidity,
		))
	}
	if winner.SpreadScore > loser.SpreadScore {
		advantages = append(advantages, fmt.Sprintf(
			"tighter spread (%.4f vs %.4f)",
			winnerMkt.Spread, loserMkt.Spread,
		))
	}
	if len(advantages) > 0 {
		parts = append(parts, "advantages: "+strings.Join(advantages, ", "))
	}

	return strings.Join(parts, "; ")
}
