package tools_test

import (
	"testing"
	"time"

	"github.com/equinox/equivalence/tools"
	"github.com/equinox/models"
)

func TestCheckDateAlignmentExactMatch(t *testing.T) {
	tool := tools.NewCheckDateAlignment()
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)

	marketA := makeMarket("will democrats control the house 2026", date)
	marketB := makeMarket("do democrats win house majority 2026", date)

	result, err := tool.Execute(marketA, marketB)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Result {
		t.Errorf("Result = false, want true for exact same date")
	}
	if result.Confidence < 0.99 {
		t.Errorf("Confidence = %v, want >= 0.99 for exact date match", result.Confidence)
	}
}

func TestCheckDateAlignmentOneDayApart(t *testing.T) {
	tool := tools.NewCheckDateAlignment()
	dateA := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	dateB := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)

	marketA := makeMarket("will gop win senate 2026", dateA)
	marketB := makeMarket("will republicans take senate 2026", dateB)

	result, err := tool.Execute(marketA, marketB)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Result {
		t.Errorf("Result = false, want true — 1 day apart is within alignment tolerance")
	}
	if result.Confidence < 0.85 {
		t.Errorf("Confidence = %v, want >= 0.85 for 1-day-apart dates", result.Confidence)
	}
}

func TestCheckDateAlignmentMonthsApart(t *testing.T) {
	tool := tools.NewCheckDateAlignment()
	dateA := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	dateB := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	marketA := makeMarket("will fed raise rates", dateA)
	marketB := makeMarket("will fed raise rates", dateB)

	result, err := tool.Execute(marketA, marketB)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Result {
		t.Errorf("Result = true, want false — 5 months apart is not aligned")
	}
	if result.Confidence > 0.1 {
		t.Errorf("Confidence = %v, want near 0 for dates months apart", result.Confidence)
	}
}

func TestCheckDateAlignmentMissingDate(t *testing.T) {
	tool := tools.NewCheckDateAlignment()
	future := time.Now().Add(180 * 24 * time.Hour)

	// Zero time on marketB simulates a missing / unparsed resolution date.
	marketA := makeMarket("will bitcoin exceed 100k 2026", future)
	marketB := models.Market{
		ID:        "test-id",
		Venue:     "polymarket",
		Title:     "will bitcoin exceed 100k 2026",
		ResolvesAt: time.Time{}, // zero value = missing date
		FetchedAt:  time.Now(),
		Status:    "open",
	}

	result, err := tool.Execute(marketA, marketB)
	if err != nil {
		t.Fatalf("Execute() error = %v, want graceful handling", err)
	}
	// When a date is missing we cannot confirm alignment, so Result=false
	// and confidence stays at 0; no error is returned.
	if result.Result {
		t.Errorf("Result = true, want false — missing date cannot confirm alignment")
	}
	if result.Confidence != 0.0 {
		t.Errorf("Confidence = %v, want 0.0 for missing date", result.Confidence)
	}
}

func TestCheckDateAlignmentWithinSevenDays(t *testing.T) {
	tool := tools.NewCheckDateAlignment()
	dateA := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	dateB := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC) // 5 days later

	marketA := makeMarket("will democrats win house 2026", dateA)
	marketB := makeMarket("will democrats take house majority 2026", dateB)

	result, err := tool.Execute(marketA, marketB)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !result.Result {
		t.Errorf("Result = false, want true — 5 days apart is within the 7-day window")
	}
	if result.Confidence < 0.60 {
		t.Errorf("Confidence = %v, want >= 0.60 for dates within 7 days", result.Confidence)
	}
}
