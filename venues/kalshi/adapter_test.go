package kalshi_test

import (
	"math"
	"testing"
	"time"

	"github.com/equinox/venues/kalshi"
)

// validKalshiMarket returns a minimal KalshiMarket leg that passes all adapter
// checks. Individual test cases override specific fields to exercise one behaviour.
func validKalshiMarket() kalshi.KalshiMarket {
	return kalshi.KalshiMarket{
		Ticker:        "KXTEST-26NOV01-Y",
		EventTicker:   "KXTEST",
		YesSubTitle:   "Will the test pass?",
		YesBidDollars: "0.44",
		YesAskDollars: "0.46",
		CloseTime:     "2026-11-01T00:00:00Z",
		Status:        "active",
	}
}

// validKalshiEvent returns a minimal KalshiEvent that wraps validKalshiMarket.
// Category is intentionally empty so tests that set raw.EventTicker exercise
// the ticker-heuristic fallback path in the adapter.
func validKalshiEvent() kalshi.KalshiEvent {
	return kalshi.KalshiEvent{
		EventTicker: "KXTEST",
		Title:       "Test Market Event",
		Category:    "",
	}
}

func approxEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

// ---------------------------------------------------------------------------
// TestKalshiAdapterNormalizesPrice
// ---------------------------------------------------------------------------

func TestKalshiAdapterNormalizesPrice(t *testing.T) {
	tests := []struct {
		name       string
		bid        string
		ask        string
		wantYesBid float64
		wantYesAsk float64
	}{
		{
			name:       "parses standard four-decimal bid and ask",
			bid:        "0.4400",
			ask:        "0.4600",
			wantYesBid: 0.44,
			wantYesAsk: 0.46,
		},
		{
			name:       "parses high-probability market",
			bid:        "0.8900",
			ask:        "0.9100",
			wantYesBid: 0.89,
			wantYesAsk: 0.91,
		},
		{
			name:       "parses boundary prices",
			bid:        "0.0100",
			ask:        "0.0300",
			wantYesBid: 0.01,
			wantYesAsk: 0.03,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validKalshiMarket()
			raw.YesBidDollars = tt.bid
			raw.YesAskDollars = tt.ask

			m, err := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw)
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
// TestKalshiAdapterCalculatesSpread
// ---------------------------------------------------------------------------

func TestKalshiAdapterCalculatesSpread(t *testing.T) {
	tests := []struct {
		name       string
		bid        string
		ask        string
		wantSpread float64
	}{
		{"2-cent spread", "0.44", "0.46", 0.02},
		{"tight 1-cent spread", "0.49", "0.50", 0.01},
		{"wide 10-cent spread", "0.40", "0.50", 0.10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validKalshiMarket()
			raw.YesBidDollars = tt.bid
			raw.YesAskDollars = tt.ask

			m, err := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw)
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
// TestKalshiAdapterCalculatesMidpoint
// ---------------------------------------------------------------------------

func TestKalshiAdapterCalculatesMidpoint(t *testing.T) {
	tests := []struct {
		name        string
		bid         string
		ask         string
		wantMid     float64
		wantNoPrice float64
	}{
		{
			name:        "midpoint of 0.44/0.46 is 0.45",
			bid:         "0.44",
			ask:         "0.46",
			wantMid:     0.45,
			wantNoPrice: 0.55,
		},
		{
			name:        "midpoint of 0.60/0.80 is 0.70",
			bid:         "0.60",
			ask:         "0.80",
			wantMid:     0.70,
			wantNoPrice: 0.30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validKalshiMarket()
			raw.YesBidDollars = tt.bid
			raw.YesAskDollars = tt.ask

			m, err := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !approxEqual(m.YesMid, tt.wantMid, 1e-9) {
				t.Errorf("YesMid = %v, want %v", m.YesMid, tt.wantMid)
			}
			if !approxEqual(m.NoPrice, tt.wantNoPrice, 1e-9) {
				t.Errorf("NoPrice = %v, want %v", m.NoPrice, tt.wantNoPrice)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestKalshiAdapterHandlesMissingFields
// ---------------------------------------------------------------------------

func TestKalshiAdapterHandlesMissingFields(t *testing.T) {
	t.Run("empty bid and ask default to zero without error", func(t *testing.T) {
		raw := validKalshiMarket()
		raw.YesBidDollars = ""
		raw.YesAskDollars = ""

		m, err := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.YesBid != 0 {
			t.Errorf("YesBid = %v, want 0", m.YesBid)
		}
		if m.YesAsk != 0 {
			t.Errorf("YesAsk = %v, want 0", m.YesAsk)
		}
	})

	t.Run("empty ticker returns error", func(t *testing.T) {
		raw := validKalshiMarket()
		raw.Ticker = ""

		_, err := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw)
		if err == nil {
			t.Fatal("expected error for empty ticker, got nil")
		}
	})

	t.Run("empty open_interest_fp defaults liquidity to zero", func(t *testing.T) {
		raw := validKalshiMarket()
		raw.OpenInterestFp = ""

		m, err := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Liquidity != 0 {
			t.Errorf("Liquidity = %v, want 0", m.Liquidity)
		}
	})
}

// ---------------------------------------------------------------------------
// TestKalshiAdapterParsesDate
// ---------------------------------------------------------------------------

func TestKalshiAdapterParsesDate(t *testing.T) {
	t.Run("parses valid RFC3339 close_time", func(t *testing.T) {
		raw := validKalshiMarket()
		raw.CloseTime = "2026-11-01T00:00:00Z"

		m, err := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		if !m.ResolvesAt.Equal(want) {
			t.Errorf("ResolvesAt = %v, want %v", m.ResolvesAt, want)
		}
	})

	t.Run("invalid close_time returns EquinoxError", func(t *testing.T) {
		raw := validKalshiMarket()
		raw.CloseTime = "not-a-date"

		_, err := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw)
		if err == nil {
			t.Fatal("expected error for invalid close_time, got nil")
		}
	})

	t.Run("empty close_time returns EquinoxError", func(t *testing.T) {
		raw := validKalshiMarket()
		raw.CloseTime = ""

		_, err := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw)
		if err == nil {
			t.Fatal("expected error for empty close_time, got nil")
		}
	})
}

// ---------------------------------------------------------------------------
// TestKalshiAdapterSetsVenue
// ---------------------------------------------------------------------------

func TestKalshiAdapterSetsVenue(t *testing.T) {
	m, err := kalshi.AdaptKalshiMarket(validKalshiEvent(), validKalshiMarket())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Venue != "kalshi" {
		t.Errorf("Venue = %q, want %q", m.Venue, "kalshi")
	}
	if m.VenueID != "KXTEST-26NOV01-Y" {
		t.Errorf("VenueID = %q, want %q", m.VenueID, "KXTEST-26NOV01-Y")
	}
}

// ---------------------------------------------------------------------------
// TestKalshiAdapterPreservesRawData
// ---------------------------------------------------------------------------

func TestKalshiAdapterPreservesRawData(t *testing.T) {
	raw := validKalshiMarket()
	m, err := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.RawData == nil {
		t.Fatal("RawData is nil, want the original KalshiMarket")
	}
	preserved, ok := m.RawData.(kalshi.KalshiMarket)
	if !ok {
		t.Fatalf("RawData type = %T, want kalshi.KalshiMarket", m.RawData)
	}
	if preserved.Ticker != raw.Ticker {
		t.Errorf("RawData.Ticker = %q, want %q", preserved.Ticker, raw.Ticker)
	}
}

// ---------------------------------------------------------------------------
// TestKalshiAdapterSetsCategory
// ---------------------------------------------------------------------------

func TestKalshiAdapterSetsCategory(t *testing.T) {
	tests := []struct {
		name         string
		eventTicker  string
		wantCategory string
	}{
		{"BTC event ticker maps to crypto", "KXBTCD-25DEC31", "crypto"},
		{"ETH event ticker maps to crypto", "KXETH-26JAN15", "crypto"},
		{"PRES event ticker maps to politics", "KXPRES-26", "politics"},
		{"SENATE event ticker maps to politics", "KXSENATE-26", "politics"},
		{"unknown event ticker maps to other", "KXWEATHER-26", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Pass an event with empty Category so the ticker-heuristic fallback runs.
			event := validKalshiEvent()
			event.Category = ""
			raw := validKalshiMarket()
			raw.EventTicker = tt.eventTicker

			m, err := kalshi.AdaptKalshiMarket(event, raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Category != tt.wantCategory {
				t.Errorf("Category = %q, want %q", m.Category, tt.wantCategory)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestKalshiAdapterCategoryFromEvent
// ---------------------------------------------------------------------------

func TestKalshiAdapterCategoryFromEvent(t *testing.T) {
	// When the event carries a Category, it takes priority over ticker heuristics.
	event := validKalshiEvent()
	event.Category = "Economics"
	raw := validKalshiMarket()
	raw.EventTicker = "KXBTCD-25DEC31" // would map to "crypto" via heuristic

	m, err := kalshi.AdaptKalshiMarket(event, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Category != "economics" {
		t.Errorf("Category = %q, want %q", m.Category, "economics")
	}
}

// ---------------------------------------------------------------------------
// TestKalshiAdapterTitleComposition
// ---------------------------------------------------------------------------

func TestKalshiAdapterTitleComposition(t *testing.T) {
	t.Run("event title + subtitle produces combined title", func(t *testing.T) {
		event := validKalshiEvent()
		event.Title = "Federal Reserve Interest Rate Decision"
		raw := validKalshiMarket()
		raw.YesSubTitle = ">5.25%"

		m, err := kalshi.AdaptKalshiMarket(event, raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// normalizeTitle lowercases and strips punctuation; both segments must appear.
		if !containsAll(m.Title, "federal reserve interest rate decision", "525") {
			t.Errorf("Title %q does not contain expected tokens", m.Title)
		}
	})

	t.Run("event title only when subtitle is empty", func(t *testing.T) {
		event := validKalshiEvent()
		event.Title = "Will it rain tomorrow"
		raw := validKalshiMarket()
		raw.YesSubTitle = ""

		m, err := kalshi.AdaptKalshiMarket(event, raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !containsAll(m.Title, "will it rain tomorrow") {
			t.Errorf("Title %q does not contain event title tokens", m.Title)
		}
	})
}

func containsAll(s string, tokens ...string) bool {
	for _, tok := range tokens {
		if !func() bool {
			for i := 0; i <= len(s)-len(tok); i++ {
				if s[i:i+len(tok)] == tok {
					return true
				}
			}
			return false
		}() {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// TestKalshiAdapterStatusMapping
// ---------------------------------------------------------------------------

func TestKalshiAdapterStatusMapping(t *testing.T) {
	tests := []struct {
		kalshiStatus string
		wantStatus   string
	}{
		{"active", "open"},
		{"initialized", "pending"},
		{"inactive", "pending"},
		{"closed", "closed"},
		{"determined", "closed"},
		{"finalized", "closed"},
		{"disputed", "closed"},
		{"amended", "closed"},
	}

	for _, tt := range tests {
		t.Run(tt.kalshiStatus, func(t *testing.T) {
			raw := validKalshiMarket()
			raw.Status = tt.kalshiStatus

			m, err := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw)
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
// TestKalshiAdapterGeneratesID
// ---------------------------------------------------------------------------

func TestKalshiAdapterGeneratesID(t *testing.T) {
	t.Run("same ticker produces same ID", func(t *testing.T) {
		m1, _ := kalshi.AdaptKalshiMarket(validKalshiEvent(), validKalshiMarket())
		m2, _ := kalshi.AdaptKalshiMarket(validKalshiEvent(), validKalshiMarket())
		if m1.ID != m2.ID {
			t.Errorf("IDs differ for same ticker: %q vs %q", m1.ID, m2.ID)
		}
		if m1.ID == "" {
			t.Error("ID is empty")
		}
	})

	t.Run("different tickers produce different IDs", func(t *testing.T) {
		raw1 := validKalshiMarket()
		raw2 := validKalshiMarket()
		raw2.Ticker = "KXOTHER-26JAN01-Y"

		m1, _ := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw1)
		m2, _ := kalshi.AdaptKalshiMarket(validKalshiEvent(), raw2)
		if m1.ID == m2.ID {
			t.Error("different tickers should produce different IDs")
		}
	})
}
