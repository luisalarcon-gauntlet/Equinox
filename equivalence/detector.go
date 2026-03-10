package equivalence

import (
	"context"
	"fmt"
	"sync"
	"time"

	aipackage "github.com/equinox/ai"
	"github.com/equinox/equivalence/tools"
	"github.com/equinox/logger"
	"github.com/equinox/models"
)

// Tier thresholds for the three-tier pre-filter.
// Scores below tierRejectCeiling are definitively unrelated — no AI call.
// Scores above tierAcceptFloor are definitively the same market — no AI call.
// Everything in between is genuinely ambiguous and goes to AI.
const (
	tierRejectCeiling = 0.25
	tierAcceptFloor   = 0.70
	aiConcurrency     = 5 // max simultaneous AI calls (semaphore slots)
)

// AIEvaluator is the interface the Detector uses to call the AI layer.
// Defining the interface here (rather than importing ai.AnthropicClient directly)
// allows tests to inject a mock without importing the live AI package.
type AIEvaluator interface {
	EvaluateEquivalence(ctx context.Context, marketA, marketB models.Market, toolResults []tools.ToolResult) (aipackage.EquivalenceResult, error)
}

// Detector orchestrates the hybrid equivalence detection pipeline.
//
// Three-tier evaluation (AI is the tiebreaker, never the workhorse):
//
//	Tier 1 — score < 0.25 : definite reject  → method="heuristic_reject", no AI call
//	Tier 2 — 0.25–0.70    : ambiguous zone   → run tools + AI, method="heuristic+ai"
//	Tier 3 — score > 0.70 : definite accept  → method="heuristic_accept", no AI call
//
// Simultaneous AI calls are throttled by a semaphore (aiConcurrency slots).
// Pairs in tiers 1 and 3 never touch the semaphore, so they never block.
//
// Layer separation: Detector never touches venue APIs or routing logic.
type Detector struct {
	heuristic *HeuristicMatcher
	aiClient  AIEvaluator
	allTools  []tools.Tool
	threshold float64
	sem       chan struct{} // throttles simultaneous AI calls — not a hard cap
	log       *logger.Logger
}

// NewDetector constructs a Detector with all five equivalence tools pre-registered.
func NewDetector(threshold float64, aiClient AIEvaluator, log *logger.Logger) *Detector {
	return &Detector{
		heuristic: NewHeuristicMatcher(threshold),
		aiClient:  aiClient,
		allTools: []tools.Tool{
			tools.NewCheckOpposites(),
			tools.NewCheckSynonyms(),
			tools.NewCheckEntityMatch(),
			tools.NewCheckDateAlignment(),
			tools.NewCheckStructural(),
		},
		threshold: threshold,
		sem:       make(chan struct{}, aiConcurrency),
		log:       log,
	}
}

// Detect runs the three-tier equivalence pipeline for a single market pair.
// It always returns a MatchResult — errors from the AI layer are absorbed and
// surfaced as warnings inside the result (graceful degradation).
func (d *Detector) Detect(ctx context.Context, marketA, marketB models.Market) (models.MatchResult, error) {
	hResult := d.heuristic.Match(marketA, marketB)
	score := hResult.Confidence

	tier, tierLabel := d.classifyTier(score)
	d.log.Info("equivalence", "", fmt.Sprintf(
		"pair scored %.2f → tier %d (%s)", score, tier, tierLabel,
	))

	switch tier {
	case 1:
		return d.tierRejectResult(marketA, marketB, hResult), nil
	case 3:
		return d.tierAcceptResult(marketA, marketB, hResult), nil
	}

	// Tier 2: acquire a semaphore slot before calling AI.
	// This queues concurrent goroutines rather than dropping them — total
	// throughput is unlimited, but simultaneous API calls are capped at aiConcurrency.
	select {
	case d.sem <- struct{}{}:
	case <-ctx.Done():
		d.log.Warn("equivalence", "anthropic", "context cancelled while waiting for AI slot — returning heuristic-only result")
		return d.fallbackResult(hResult), nil
	}
	defer func() { <-d.sem }()

	toolResults := d.runToolsConcurrently(ctx, marketA, marketB)

	aiResult, err := d.aiClient.EvaluateEquivalence(ctx, marketA, marketB, toolResults)
	if err != nil {
		d.log.Warn("equivalence", "anthropic", "AI layer unavailable — returning heuristic-only result")
		d.log.Error("equivalence", "anthropic", "EvaluateEquivalence failed", err)
		return d.fallbackResult(hResult), nil
	}

	return d.buildAIResult(marketA, marketB, hResult, aiResult), nil
}

// classifyTier maps a heuristic score to a tier number and a short label
// used in log output and the per-pair debug line.
func (d *Detector) classifyTier(score float64) (tier int, label string) {
	switch {
	case score < tierRejectCeiling:
		return 1, "heuristic_reject"
	case score > tierAcceptFloor:
		return 3, "heuristic_accept"
	default:
		return 2, "ai_evaluation"
	}
}

// tierRejectResult returns a definite-reject MatchResult for tier 1 pairs.
// Confidence 0.95 reflects strong heuristic certainty that these markets are unrelated.
func (d *Detector) tierRejectResult(marketA, marketB models.Market, h models.MatchResult) models.MatchResult {
	return models.MatchResult{
		MarketA:    marketA,
		MarketB:    marketB,
		IsMatch:    false,
		Confidence: 0.95,
		Method:     "heuristic_reject",
		Reasoning:  h.Reasoning,
		MatchedAt:  time.Now(),
	}
}

// tierAcceptResult returns a definite-accept MatchResult for tier 3 pairs.
// Confidence 0.90 reflects strong heuristic certainty that these markets are equivalent.
func (d *Detector) tierAcceptResult(marketA, marketB models.Market, h models.MatchResult) models.MatchResult {
	return models.MatchResult{
		MarketA:    marketA,
		MarketB:    marketB,
		IsMatch:    true,
		Confidence: 0.90,
		Method:     "heuristic_accept",
		Reasoning:  h.Reasoning,
		MatchedAt:  time.Now(),
	}
}

// runToolsConcurrently executes all registered tools in parallel and collects
// their results. Tool errors are logged but do not fail the overall evaluation —
// a failed tool simply produces no result for Claude to consider.
func (d *Detector) runToolsConcurrently(ctx context.Context, marketA, marketB models.Market) []tools.ToolResult {
	type indexed struct {
		idx    int
		result tools.ToolResult
	}

	ch := make(chan indexed, len(d.allTools))
	var wg sync.WaitGroup

	for i, tool := range d.allTools {
		wg.Add(1)
		go func(idx int, t tools.Tool) {
			defer wg.Done()
			result, err := t.Execute(marketA, marketB)
			if err != nil {
				d.log.Error("equivalence", "", fmt.Sprintf("tool %s failed", t.Name()), err)
				return
			}
			ch <- indexed{idx: idx, result: result}
		}(i, tool)
	}

	wg.Wait()
	close(ch)

	// Re-order results by original tool index for deterministic prompt ordering.
	ordered := make([]tools.ToolResult, len(d.allTools))
	count := 0
	for item := range ch {
		ordered[item.idx] = item.result
		count++
	}
	return ordered[:count]
}

// fallbackResult converts a heuristic result into a "heuristic-only" result
// with a warning that the AI layer was unavailable.
//
// FallbackThreshold: if heuristic confidence >= 0.50 we still call it a match
// to avoid false negatives when AI is down.
const fallbackThreshold = 0.50

func (d *Detector) fallbackResult(h models.MatchResult) models.MatchResult {
	return models.MatchResult{
		MarketA:    h.MarketA,
		MarketB:    h.MarketB,
		IsMatch:    h.Confidence >= fallbackThreshold,
		Confidence: h.Confidence,
		Method:     "heuristic-only",
		Reasoning:  h.Reasoning,
		Warnings:   []string{"AI layer unavailable — result based on heuristics only"},
		MatchedAt:  time.Now(),
	}
}

// DebugTopPairs delegates to the underlying HeuristicMatcher.
// Call this after you have collected both venue market slices to print the
// top topN highest-scoring cross-product pairs to stderr.
//
// THIS IS TEMPORARY DEBUG INSTRUMENTATION — remove before any production use.
func (d *Detector) DebugTopPairs(marketsA, marketsB []models.Market, topN int) {
	d.heuristic.DebugTopPairs(marketsA, marketsB, topN)
}

// buildAIResult merges the heuristic run and the AI synthesis into a final MatchResult.
func (d *Detector) buildAIResult(
	marketA, marketB models.Market,
	hResult models.MatchResult,
	aiResult aipackage.EquivalenceResult,
) models.MatchResult {
	d.log.Info("equivalence", "", fmt.Sprintf(
		"AI result: equivalent=%v opposites=%v confidence=%.2f",
		aiResult.IsEquivalent, aiResult.AreOpposites, aiResult.Confidence,
	))

	reasoning := fmt.Sprintf(
		"[heuristic] %s | [ai] %s",
		hResult.Reasoning, aiResult.Reasoning,
	)

	return models.MatchResult{
		MarketA:      marketA,
		MarketB:      marketB,
		IsMatch:      aiResult.IsEquivalent,
		AreOpposites: aiResult.AreOpposites,
		Confidence:   aiResult.Confidence,
		Method:       "heuristic+ai",
		Reasoning:    reasoning,
		MatchedAt:    time.Now(),
	}
}
