package equivalence_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/equinox/ai"
	"github.com/equinox/equivalence"
	"github.com/equinox/equivalence/tools"
	"github.com/equinox/logger"
	"github.com/equinox/models"
)

// mockAIClient satisfies the equivalence.AIEvaluator interface for tests.
type mockAIClient struct {
	result ai.EquivalenceResult
	err    error
	called bool
}

func (m *mockAIClient) EvaluateEquivalence(
	_ context.Context,
	_ models.Market,
	_ models.Market,
	_ []tools.ToolResult,
) (ai.EquivalenceResult, error) {
	m.called = true
	return m.result, m.err
}

func (m *mockAIClient) EvaluateBatch(
	_ context.Context,
	pairs []ai.BatchPair,
) ([]ai.EquivalenceResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	results := make([]ai.EquivalenceResult, len(pairs))
	for i := range pairs {
		m.called = true
		results[i] = m.result
	}
	return results, nil
}

func detectorTestMarket(title string, resolvesAt time.Time) models.Market {
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
		ResolvesAt: resolvesAt,
		FetchedAt:  time.Now(),
		Status:     "open",
	}
}

func TestDetectorRejectsLowScoringPairWithoutAI(t *testing.T) {
	log := logger.New(io.Discard)
	mock := &mockAIClient{}

	// Completely unrelated titles with zero dates → entity overlap = 0, date score = 0
	// → combined score = 0.0 < tierRejectCeiling (0.25) → tier-1 hard reject.
	// AI must NOT be called.
	detector := equivalence.NewDetector(0.80, mock, log)
	marketA := detectorTestMarket("will btc exceed 100k by year end", time.Time{})
	marketB := detectorTestMarket("who wins the democratic primary", time.Time{})

	result, err := detector.Detect(context.Background(), marketA, marketB)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if result.IsMatch {
		t.Errorf("IsMatch = true, want false for definitively unrelated titles")
	}
	if result.Method != "heuristic_reject" {
		t.Errorf("Method = %q, want 'heuristic_reject'", result.Method)
	}
	if mock.called {
		t.Errorf("AI layer was called — must not be called when heuristic rejects")
	}
}

func TestDetectorEscalatesToAIWhenHeuristicLow(t *testing.T) {
	log := logger.New(io.Discard)
	mock := &mockAIClient{
		result: ai.EquivalenceResult{
			IsMatch:     true,
			Confidence:  0.93,
			Reasoning:   "same House race",
			UsedAILayer: true,
		},
	}

	// Different entities (gop vs democrats) + different wording → heuristic < 0.80
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	detector := equivalence.NewDetector(0.80, mock, log)

	marketA := detectorTestMarket("will the gop control the house after 2026 midterms", date)
	marketB := detectorTestMarket("will democrats control the house 2026", date)

	result, err := detector.Detect(context.Background(), marketA, marketB)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !mock.called {
		t.Errorf("AI layer was not called — should be called when heuristic confidence < threshold")
	}
	if result.Method != "heuristic+ai" {
		t.Errorf("Method = %q, want 'heuristic+ai'", result.Method)
	}
	if !result.IsMatch {
		t.Errorf("IsMatch = false, want true (AI confirmed match)")
	}
}

func TestDetectorFallsBackGracefullyWhenAIUnavailable(t *testing.T) {
	log := logger.New(io.Discard)
	mock := &mockAIClient{
		err: errors.New("connection refused"),
	}

	// Medium-confidence pair + AI failure → return heuristic-only result with warning.
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	detector := equivalence.NewDetector(0.80, mock, log)

	marketA := detectorTestMarket("will btc price exceed 100000 in 2026", date)
	marketB := detectorTestMarket("bitcoin above 100k end of year 2026", date)

	result, err := detector.Detect(context.Background(), marketA, marketB)
	// Graceful degradation — no error propagated, just a warning in the result.
	if err != nil {
		t.Fatalf("Detect() returned error = %v, want nil (graceful degradation)", err)
	}
	if result.Method != "heuristic-only" {
		t.Errorf("Method = %q, want 'heuristic-only'", result.Method)
	}
	if len(result.Warnings) == 0 {
		t.Errorf("Warnings is empty, want at least one warning about AI unavailability")
	}
}

func TestDetectorReturnsMatchedAt(t *testing.T) {
	log := logger.New(io.Discard)
	mock := &mockAIClient{}

	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	title := "will democrats control the house 2026"
	detector := equivalence.NewDetector(0.80, mock, log)

	before := time.Now()
	result, err := detector.Detect(context.Background(),
		detectorTestMarket(title, date),
		detectorTestMarket(title, date),
	)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if result.MatchedAt.Before(before) {
		t.Errorf("MatchedAt is before test start — should be set to current time")
	}
}

func TestDetectorResultHasReasoning(t *testing.T) {
	log := logger.New(io.Discard)
	mock := &mockAIClient{}

	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	title := "will democrats control the house 2026"
	detector := equivalence.NewDetector(0.80, mock, log)

	result, err := detector.Detect(context.Background(),
		detectorTestMarket(title, date),
		detectorTestMarket(title, date),
	)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if result.Reasoning == "" {
		t.Errorf("Reasoning must not be empty")
	}
}

func TestDetectorAIResultPreservesBooleanMatch(t *testing.T) {
	log := logger.New(io.Discard)
	mock := &mockAIClient{
		result: ai.EquivalenceResult{
			IsMatch:     true,
			Confidence:  0.94,
			Reasoning:   "same underlying market",
			UsedAILayer: true,
		},
	}

	// Trigger AI escalation.
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	detector := equivalence.NewDetector(0.80, mock, log)

	marketA := detectorTestMarket("will gop win senate 2026", date)
	marketB := detectorTestMarket("will democrats win senate 2026", date)

	result, err := detector.Detect(context.Background(), marketA, marketB)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !result.IsMatch {
		t.Errorf("IsMatch = false, want true — AI result boolean match must be preserved")
	}
}
