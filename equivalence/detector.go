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

// Tier thresholds for the two-tier pre-filter.
// Scores below tierRejectCeiling are definitively unrelated — no AI call.
// Everything at or above tierRejectCeiling goes to the AI layer.
const (
	tierRejectCeiling = 0.25
	aiBatchSize       = 5  // pairs sent per AI API call
	aiConcurrency     = 5  // max simultaneous AI calls for single-pair Detect
)

// AIEvaluator is the interface the Detector uses to call the AI layer.
// Defining the interface here (rather than importing ai.OpenAIClient directly)
// allows tests to inject a mock without importing the live AI package.
type AIEvaluator interface {
	// EvaluateEquivalence classifies a single pair (used by Detect).
	EvaluateEquivalence(ctx context.Context, marketA, marketB models.Market, toolResults []tools.ToolResult) (aipackage.EquivalenceResult, error)
	// EvaluateBatch classifies up to aiBatchSize pairs in one API call (used by DetectAllPairs).
	EvaluateBatch(ctx context.Context, pairs []aipackage.BatchPair) ([]aipackage.EquivalenceResult, error)
}

// Detector orchestrates the hybrid equivalence detection pipeline.
//
// Two-tier evaluation:
//
//	Tier 1 — score < 0.25 : definite reject  → method="heuristic_reject", no AI call
//	Tier 2 — score ≥ 0.25 : ambiguous zone   → run tools + AI, method="heuristic+ai"
//
// Single-pair path (Detect): AI calls are throttled by a semaphore (aiConcurrency slots).
// Bulk path (DetectAllPairs): pairs are batched aiBatchSize at a time; each batch is one API call.
//
// Layer separation: Detector never touches venue APIs or routing logic.
type Detector struct {
	heuristic *HeuristicMatcher
	aiClient  AIEvaluator
	allTools  []tools.Tool
	threshold float64
	sem       chan struct{} // throttles simultaneous AI calls in single-pair Detect
	log       *logger.Logger
}

// NewDetector constructs a Detector with all equivalence tools pre-registered.
func NewDetector(threshold float64, aiClient AIEvaluator, log *logger.Logger) *Detector {
	return &Detector{
		heuristic: NewHeuristicMatcher(threshold),
		aiClient:  aiClient,
		allTools: []tools.Tool{
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

// Detect runs the two-tier equivalence pipeline for a single market pair.
// It always returns a MatchResult — errors from the AI layer are absorbed and
// surfaced as warnings inside the result (graceful degradation).
// Use DetectAllPairs for bulk evaluation; it batches AI calls more efficiently.
func (d *Detector) Detect(ctx context.Context, marketA, marketB models.Market) (models.MatchResult, error) {
	hResult := d.heuristic.Match(marketA, marketB)

	if hResult.Confidence < tierRejectCeiling {
		return d.tierRejectResult(marketA, marketB, hResult), nil
	}

	// Acquire a semaphore slot before calling AI.
	select {
	case d.sem <- struct{}{}:
	case <-ctx.Done():
		d.log.Warn("equivalence", "openai", "context cancelled while waiting for AI slot — returning heuristic-only result")
		return d.fallbackResult(hResult), nil
	}
	defer func() { <-d.sem }()

	toolResults := d.runToolsConcurrently(ctx, marketA, marketB)

	recordAIAttempt(ctx)
	aiResult, err := d.aiClient.EvaluateEquivalence(ctx, marketA, marketB, toolResults)
	if err != nil {
		d.log.Warn("equivalence", "openai", "AI layer unavailable — returning heuristic-only result")
		d.log.Error("equivalence", "openai", "EvaluateEquivalence failed", err)
		return d.fallbackResult(hResult), nil
	}

	return d.buildAIResult(marketA, marketB, hResult, aiResult), nil
}

// DetectAllPairs runs equivalence detection on a slice of pre-filtered candidate pairs.
// The two-phase pipeline:
//
//  1. Heuristics run concurrently on all pairs.
//  2. Tier-1 pairs (score < 0.25) are rejected immediately — no AI.
//  3. Tools run concurrently on every surviving tier-2 pair.
//  4. Tier-2 pairs are sent to the AI in batches of aiBatchSize (5).
//     The last batch may be smaller (e.g. 23 pairs → four batches of 5 + one of 3).
//     If a batch fails, all pairs in that batch fall back to heuristic-only results.
//
// Results are returned in the same order as the input pairs slice.
func (d *Detector) DetectAllPairs(ctx context.Context, pairs []models.MarketPair) ([]models.MatchResult, error) {
	if len(pairs) == 0 {
		return nil, nil
	}

	// ── Phase 1: run heuristics concurrently ─────────────────────────────────
	type heuristicItem struct {
		idx    int
		pair   models.MarketPair
		result models.MatchResult
	}

	hCh := make(chan heuristicItem, len(pairs))
	var wg sync.WaitGroup
	for i, p := range pairs {
		wg.Add(1)
		go func(idx int, pr models.MarketPair) {
			defer wg.Done()
			hCh <- heuristicItem{idx: idx, pair: pr, result: d.heuristic.Match(pr.A, pr.B)}
		}(i, p)
	}
	wg.Wait()
	close(hCh)

	// Allocate result slice indexed by original pair position.
	finalResults := make([]models.MatchResult, len(pairs))
	var tier2 []heuristicItem

	for item := range hCh {
		if item.result.Confidence < tierRejectCeiling {
			finalResults[item.idx] = d.tierRejectResult(item.pair.A, item.pair.B, item.result)
		} else {
			tier2 = append(tier2, item)
		}
	}

	if len(tier2) == 0 {
		return finalResults, nil
	}

	// ── Phase 2: run tools concurrently on all tier-2 pairs ──────────────────
	type readyItem struct {
		idx         int
		pair        models.MarketPair
		hResult     models.MatchResult
		toolResults []tools.ToolResult
	}

	toolCh := make(chan readyItem, len(tier2))
	for _, item := range tier2 {
		wg.Add(1)
		go func(hi heuristicItem) {
			defer wg.Done()
			tr := d.runToolsConcurrently(ctx, hi.pair.A, hi.pair.B)
			toolCh <- readyItem{idx: hi.idx, pair: hi.pair, hResult: hi.result, toolResults: tr}
		}(item)
	}
	wg.Wait()
	close(toolCh)

	var readyForAI []readyItem
	for item := range toolCh {
		readyForAI = append(readyForAI, item)
	}

	// ── Phase 3: AI calls in batches of aiBatchSize ──────────────────────────
	for i := 0; i < len(readyForAI); i += aiBatchSize {
		end := i + aiBatchSize
		if end > len(readyForAI) {
			end = len(readyForAI) // last batch may be smaller than aiBatchSize
		}
		batch := readyForAI[i:end]

		batchPairs := make([]aipackage.BatchPair, len(batch))
		for j, item := range batch {
			batchPairs[j] = aipackage.BatchPair{
				MarketA:     item.pair.A,
				MarketB:     item.pair.B,
				ToolResults: item.toolResults,
			}
		}

		recordAIAttempt(ctx) // one API call per batch
		batchResults, err := d.aiClient.EvaluateBatch(ctx, batchPairs)
		if err != nil {
			d.log.Warn("equivalence", "openai",
				fmt.Sprintf("batch AI call failed (%d pairs) — using heuristic fallback", len(batch)))
			d.log.Error("equivalence", "openai", "EvaluateBatch failed", err)
			for _, item := range batch {
				finalResults[item.idx] = d.fallbackResult(item.hResult)
			}
			continue
		}

		for j, item := range batch {
			finalResults[item.idx] = d.buildAIResult(item.pair.A, item.pair.B, item.hResult, batchResults[j])
		}
	}

	return finalResults, nil
}

// tierRejectResult returns a definite-reject MatchResult for tier-1 pairs.
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

// runToolsConcurrently executes all registered tools in parallel and collects
// their results. Tool errors are logged but do not fail the overall evaluation —
// a failed tool simply produces no result for the AI to consider.
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
	reasoning := fmt.Sprintf(
		"[heuristic] %s | [ai] %s",
		hResult.Reasoning, aiResult.Reasoning,
	)

	return models.MatchResult{
		MarketA:    marketA,
		MarketB:    marketB,
		IsMatch:    aiResult.IsMatch,
		Confidence: aiResult.Confidence,
		Method:     "heuristic+ai",
		Reasoning:  reasoning,
		MatchedAt:  time.Now(),
	}
}
