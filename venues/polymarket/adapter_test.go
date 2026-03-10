package polymarket_test

import (
	"math"
	"testing"
	"time"

	"github.com/equinox/venues/polymarket"
)

func approxEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

// validPolymarketMarket returns a minimal PolymarketMarket that passes all
// adapter checks. Individual test cases override specific fields.
func validPolymarketMarket() polymarket.PolymarketMarket {
	return polymarket.PolymarketMarket{
		ID:            "123",
		Question:      "Will the test pass?",
		OutcomePrices: `["0.65", "0.35"]`,
		BestBid:       0.64,
		BestAsk:       0.66,
		Spread:        0.02,
		EndDate:       "2026-11-01T00:00:00Z",
		Active:        true,
		Closed:        false,
		LiquidityNum:  10000,
		Volume24hr:    5000,
		Category:      "politics",
	}
}

// ---------------------------------------------------------------------------
// TestPolymarketAdapterNormalizesPrice — outcomePrices[0] string → float64
// ---------------------------------------------------------------------------

func TestPolymarketAdapterNormalizesPrice(t *testing.T) {
	tests := []struct {
		name          string
		outcomePrices string
		bestBid       float64
		bestAsk       float64
		wantYesBid    float64
		wantYesAsk    float64
	}{
		{
			name:          "uses bestBid/bestAsk when available",
			outcomePrices: `["0.65", "0.35"]`,
			bestBid:       0.64,
			bestAsk:       0.66,
			wantYesBid:    0.64,
			wantYesAsk:    0.66,
		},
		{
			name:          "falls back to outcomePrices[0] when bestBid=bestAsk=0",
			outcomePrices: `["0.70", "0.30"]`,
			bestBid:       0,
			bestAsk:       0,
			wantYesBid:    0.70,
			wantYesAsk:    0.70,
		},
		{
			name:          "parses outcomePrices with high precision",
			outcomePrices: `["0.5500", "0.4500"]`,
			bestBid:       0,
			bestAsk:       0,
			wantYesBid:    0.55,
			wantYesAsk:    0.55,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validPolymarketMarket()
			raw.OutcomePrices = tt.outcomePrices
			raw.BestBid = tt.bestBid
			raw.BestAsk = tt.bestAsk

			m, err := polymarket.AdaptPolymarketMarket(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !approxEqual(m.YesBid, tt.wantYesBid, 1e-9) {
				t.Errorf("YesBid = %v, want %v", m.YesBid, tt.wantYesBid)
			}
			if !approxEqual(m.YesAsk, tt.wantYesAsk, 1e-9) {
				t.Errorf("YesAsk = %v, want %v", m.YesAsk, tt.wantYesAsk)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestPolymarketAdapterCalculatesSpread
// ---------------------------------------------------------------------------

func TestPolymarketAdapterCalculatesSpread(t *testing.T) {
	tests := []struct {
		name       string
		bestBid    float64
		bestAsk    float64
		wantSpread float64
	}{
		{"2-cent spread", 0.48, 0.50, 0.02},
		{"tight 1-cent spread", 0.495, 0.505, 0.01},
		{"wide 10-cent spread", 0.45, 0.55, 0.10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validPolymarketMarket()
			raw.BestBid = tt.bestBid
			raw.BestAsk = tt.bestAsk

			m, err := polymarket.AdaptPolymarketMarket(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !approxEqual(m.Spread, tt.wantSpread, 1e-9) {
				t.Errorf("Spread = %v, want %v", m.Spread, tt.wantSpread)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestPolymarketAdapterHandlesMissingFields
// ---------------------------------------------------------------------------

func TestPolymarketAdapterHandlesMissingFields(t *testing.T) {
	t.Run("empty ID returns error", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.ID = ""

		_, err := polymarket.AdaptPolymarketMarket(raw)
		if err == nil {
			t.Fatal("expected error for empty ID, got nil")
		}
	})

	t.Run("empty outcomePrices with zero bestBid/bestAsk defaults to zero", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.OutcomePrices = ""
		raw.BestBid = 0
		raw.BestAsk = 0

		m, err := polymarket.AdaptPolymarketMarket(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.YesBid != 0 {
			t.Errorf("YesBid = %v, want 0", m.YesBid)
		}
	})

	t.Run("zero liquidity is accepted without error", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.LiquidityNum = 0

		m, err := polymarket.AdaptPolymarketMarket(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Liquidity != 0 {
			t.Errorf("Liquidity = %v, want 0", m.Liquidity)
		}
	})
}

// ---------------------------------------------------------------------------
// TestPolymarketAdapterParsesDate
// ---------------------------------------------------------------------------

func TestPolymarketAdapterParsesDate(t *testing.T) {
	t.Run("parses valid RFC3339 endDate", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.EndDate = "2026-11-04T00:00:00Z"

		m, err := polymarket.AdaptPolymarketMarket(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
		if !m.ResolvesAt.Equal(want) {
			t.Errorf("ResolvesAt = %v, want %v", m.ResolvesAt, want)
		}
	})

	t.Run("falls back to endDateIso when endDate is empty", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.EndDate = ""
		raw.EndDateIso = "2026-11-04"

		m, err := polymarket.AdaptPolymarketMarket(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
		if !m.ResolvesAt.Equal(want) {
			t.Errorf("ResolvesAt = %v, want %v", m.ResolvesAt, want)
		}
	})

	// A malformed endDate no longer causes a hard error; the adapter falls
	// through to other date fields, then to a zero ResolvesAt with a warning.
	t.Run("malformed endDate with no fallbacks yields zero ResolvesAt without error", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.EndDate = "not-a-date"
		raw.EndDateIso = ""

		m, err := polymarket.AdaptPolymarketMarket(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !m.ResolvesAt.IsZero() {
			t.Errorf("ResolvesAt = %v, want zero time", m.ResolvesAt)
		}
	})

	t.Run("falls back to closedTime when endDate and endDateIso are empty", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.EndDate = ""
		raw.EndDateIso = ""
		raw.ClosedTime = "2026-12-01T06:00:00Z"

		m, err := polymarket.AdaptPolymarketMarket(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 12, 1, 6, 0, 0, 0, time.UTC)
		if !m.ResolvesAt.Equal(want) {
			t.Errorf("ResolvesAt = %v, want %v", m.ResolvesAt, want)
		}
	})

	t.Run("falls back to expiryDate when earlier fields are empty", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.EndDate = ""
		raw.EndDateIso = ""
		raw.ClosedTime = ""
		raw.ExpiryDate = "2026-09-15"

		m, err := polymarket.AdaptPolymarketMarket(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
		if !m.ResolvesAt.Equal(want) {
			t.Errorf("ResolvesAt = %v, want %v", m.ResolvesAt, want)
		}
	})

	t.Run("falls back to resolveTime when earlier fields are empty", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.EndDate = ""
		raw.EndDateIso = ""
		raw.ClosedTime = ""
		raw.ExpiryDate = ""
		raw.ResolveTime = "2026-10-31T12:00:00Z"

		m, err := polymarket.AdaptPolymarketMarket(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
		if !m.ResolvesAt.Equal(want) {
			t.Errorf("ResolvesAt = %v, want %v", m.ResolvesAt, want)
		}
	})

	t.Run("all date fields empty yields zero ResolvesAt without error", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.EndDate = ""
		raw.EndDateIso = ""
		raw.ClosedTime = ""
		raw.ExpiryDate = ""
		raw.ResolveTime = ""

		m, err := polymarket.AdaptPolymarketMarket(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !m.ResolvesAt.IsZero() {
			t.Errorf("ResolvesAt = %v, want zero time", m.ResolvesAt)
		}
	})
}

// ---------------------------------------------------------------------------
// TestPolymarketAdapterSetsVenue
// ---------------------------------------------------------------------------

func TestPolymarketAdapterSetsVenue(t *testing.T) {
	m, err := polymarket.AdaptPolymarketMarket(validPolymarketMarket())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Venue != "polymarket" {
		t.Errorf("Venue = %q, want %q", m.Venue, "polymarket")
	}
	if m.VenueID != "123" {
		t.Errorf("VenueID = %q, want %q", m.VenueID, "123")
	}
}

// ---------------------------------------------------------------------------
// TestPolymarketAdapterPreservesRawData
// ---------------------------------------------------------------------------

func TestPolymarketAdapterPreservesRawData(t *testing.T) {
	raw := validPolymarketMarket()
	m, err := polymarket.AdaptPolymarketMarket(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.RawData == nil {
		t.Fatal("RawData is nil, want the original PolymarketMarket")
	}
	preserved, ok := m.RawData.(polymarket.PolymarketMarket)
	if !ok {
		t.Fatalf("RawData type = %T, want polymarket.PolymarketMarket", m.RawData)
	}
	if preserved.ID != raw.ID {
		t.Errorf("RawData.ID = %q, want %q", preserved.ID, raw.ID)
	}
}

// ---------------------------------------------------------------------------
// TestPolymarketAdapterExtractsCategory — from category field or tags fallback
// ---------------------------------------------------------------------------

func TestPolymarketAdapterExtractsCategory(t *testing.T) {
	t.Run("uses category field directly", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.Category = "sports"

		m, err := polymarket.AdaptPolymarketMarket(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Category != "sports" {
			t.Errorf("Category = %q, want %q", m.Category, "sports")
		}
	})

	t.Run("falls back to first tag when category is empty", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.Category = ""
		raw.Tags = []string{"crypto", "bitcoin"}

		m, err := polymarket.AdaptPolymarketMarket(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Category != "crypto" {
			t.Errorf("Category = %q, want %q", m.Category, "crypto")
		}
	})

	t.Run("defaults to other when category and tags are both empty", func(t *testing.T) {
		raw := validPolymarketMarket()
		raw.Category = ""
		raw.Tags = nil

		m, err := polymarket.AdaptPolymarketMarket(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Category != "other" {
			t.Errorf("Category = %q, want %q", m.Category, "other")
		}
	})
}

// ---------------------------------------------------------------------------
// TestPolymarketAdapterStatusMapping
// ---------------------------------------------------------------------------

func TestPolymarketAdapterStatusMapping(t *testing.T) {
	tests := []struct {
		name       string
		active     bool
		closed     bool
		wantStatus string
	}{
		{"active open market", true, false, "open"},
		{"closed market", true, true, "closed"},
		{"inactive closed market", false, true, "closed"},
		{"inactive open market", false, false, "open"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validPolymarketMarket()
			raw.Active = tt.active
			raw.Closed = tt.closed

			m, err := polymarket.AdaptPolymarketMarket(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", m.Status, tt.wantStatus)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestPolymarketAdapterGeneratesID
// ---------------------------------------------------------------------------

func TestPolymarketAdapterGeneratesID(t *testing.T) {
	t.Run("same ID produces same internal UUID", func(t *testing.T) {
		m1, _ := polymarket.AdaptPolymarketMarket(validPolymarketMarket())
		m2, _ := polymarket.AdaptPolymarketMarket(validPolymarketMarket())
		if m1.ID != m2.ID {
			t.Errorf("IDs differ: %q vs %q", m1.ID, m2.ID)
		}
		if m1.ID == "" {
			t.Error("ID is empty")
		}
	})

	t.Run("different IDs produce different internal UUIDs", func(t *testing.T) {
		raw1 := validPolymarketMarket()
		raw2 := validPolymarketMarket()
		raw2.ID = "9999"

		m1, _ := polymarket.AdaptPolymarketMarket(raw1)
		m2, _ := polymarket.AdaptPolymarketMarket(raw2)
		if m1.ID == m2.ID {
			t.Error("different venue IDs should produce different internal IDs")
		}
	})
}

// ---------------------------------------------------------------------------
// TestPolymarketAdapterNormalizesTitle
// ---------------------------------------------------------------------------

func TestPolymarketAdapterNormalizesTitle(t *testing.T) {
	tests := []struct {
		question  string
		wantTitle string
	}{
		{
			question:  "Will Joe Biden get Coronavirus?",
			wantTitle: "will joe biden get coronavirus",
		},
		{
			question:  "  Multiple   Spaces   Here  ",
			wantTitle: "multiple spaces here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.question, func(t *testing.T) {
			raw := validPolymarketMarket()
			raw.Question = tt.question

			m, err := polymarket.AdaptPolymarketMarket(raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", m.Title, tt.wantTitle)
			}
		})
	}
}
