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

func TestDetectorUsesHeuristicWhenConfident(t *testing.T) {
	log := logger.New(io.Discard)
	mock := &mockAIClient{}

	// Identical titles with the same date → heuristic confidence ≥ 0.80
	// → AI must NOT be called.
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	title := "will democrats control the house 2026"

	detector := equivalence.NewDetector(0.80, mock, log)
	marketA := detectorTestMarket(title, date)
	marketB := detectorTestMarket(title, date)

	result, err := detector.Detect(context.Background(), marketA, marketB)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if !result.IsMatch {
		t.Errorf("IsMatch = false, want true for identical titles")
	}
	if result.Method != "heuristic" {
		t.Errorf("Method = %q, want 'heuristic'", result.Method)
	}
	if mock.called {
		t.Errorf("AI layer was called — should not be called when heuristic is confident")
	}
}

func TestDetectorEscalatesToAIWhenHeuristicLow(t *testing.T) {
	log := logger.New(io.Discard)
	mock := &mockAIClient{
		result: ai.EquivalenceResult{
			IsEquivalent: true,
			AreOpposites: true,
			Confidence:   0.93,
			Reasoning:    "opposite sides of the same House race",
			UsedAILayer:  true,
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
	if !result.AreOpposites {
		t.Errorf("AreOpposites = false, want true")
	}
}

func TestDetectorFallsBackGracefullyWhenAIUnavailable(t *testing.T) {
	log := logger.New(io.Discard)
	mock := &mockAIClient{
		err: errors.New("connection refused"),
	}

	// Low heuristic confidence + AI failure → return heuristic result with warning.
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	detector := equivalence.NewDetector(0.80, mock, log)

	marketA := detectorTestMarket("will the gop control the house 2026", date)
	marketB := detectorTestMarket("will democrats control the house 2026", date)

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

func TestDetectorAIResultOppositesFlagPreserved(t *testing.T) {
	log := logger.New(io.Discard)
	mock := &mockAIClient{
		result: ai.EquivalenceResult{
			IsEquivalent: true,
			AreOpposites: true,
			Confidence:   0.94,
			Reasoning:    "mirror image markets",
			UsedAILayer:  true,
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
	if !result.AreOpposites {
		t.Errorf("AreOpposites = false, want true — AI result AreOpposites flag must be preserved")
	}
}
