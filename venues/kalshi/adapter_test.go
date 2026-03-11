package kalshi_test

import (
	"math"
	"testing"
	"time"

	"github.com/equinox/venues/kalshi"
)

func validKalshiMarket() kalshi.KalshiMarket {
	return kalshi.KalshiMarket{
		Ticker:        "KXTEST-26NOV01-T1",
		YesSubtitle:   "Above $80,001",
		YesBidDollars: "0.44",
		YesAskDollars: "0.46",
		CloseTS:       "2026-11-01T00:00:00Z",
		Status:        "open",
		Volume:        120400,
		CustomStrike:  map[string]string{"Price": "$80,001"},
	}
}

func validKalshiSeries() kalshi.KalshiSeriesResult {
	return kalshi.KalshiSeriesResult{
		SeriesTicker:      "KXBTCD",
		SeriesTitle:       "Bitcoin price Above/below",
		EventTicker:       "KXBTCD-26NOV01",
		EventTitle:        "Bitcoin price today at 5pm EDT?",
		EventSubtitle:     "On Nov 1, 2026 at 5pm EDT",
		Category:          "",
		TotalSeriesVolume: 1318403817,
		TotalVolume:       1481700,
		Tags:              []string{"crypto", "bitcoin"},
		TopicKeywords:     []string{"bitcoin", "btc", "price"},
	}
}

func approxEqual(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

func TestKalshiAdapterNormalizesPrice(t *testing.T) {
	tests := []struct {
		name       string
		bid        string
		ask        string
		wantYesBid float64
		wantYesAsk float64
	}{
		{"parses standard four-decimal bid and ask", "0.4400", "0.4600", 0.44, 0.46},
		{"parses high-probability market", "0.8900", "0.9100", 0.89, 0.91},
		{"parses boundary prices", "0.0100", "0.0300", 0.01, 0.03},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validKalshiMarket()
			raw.YesBidDollars = tt.bid
			raw.YesAskDollars = tt.ask

			m, err := kalshi.AdaptKalshiMarket(validKalshiSeries(), raw)
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

func TestKalshiAdapterFallsBackToCentPrices(t *testing.T) {
	raw := validKalshiMarket()
	raw.YesBidDollars = ""
	raw.YesAskDollars = ""
	raw.YesBid = 44
	raw.YesAsk = 46

	m, err := kalshi.AdaptKalshiMarket(validKalshiSeries(), raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approxEqual(m.YesBid, 0.44, 1e-9) || !approxEqual(m.YesAsk, 0.46, 1e-9) {
		t.Errorf("cent fallback prices = (%v,%v), want (0.44,0.46)", m.YesBid, m.YesAsk)
	}
}

func TestKalshiAdapterCalculatesSpreadAndMidpoint(t *testing.T) {
	raw := validKalshiMarket()
	raw.YesBidDollars = "0.44"
	raw.YesAskDollars = "0.46"

	m, err := kalshi.AdaptKalshiMarket(validKalshiSeries(), raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approxEqual(m.Spread, 0.02, 1e-9) {
		t.Errorf("Spread = %v, want 0.02", m.Spread)
	}
	if !approxEqual(m.YesMid, 0.45, 1e-9) {
		t.Errorf("YesMid = %v, want 0.45", m.YesMid)
	}
	if !approxEqual(m.NoPrice, 0.55, 1e-9) {
		t.Errorf("NoPrice = %v, want 0.55", m.NoPrice)
	}
}

func TestKalshiAdapterHandlesMissingFields(t *testing.T) {
	t.Run("empty ticker returns error", func(t *testing.T) {
		raw := validKalshiMarket()
		raw.Ticker = ""

		_, err := kalshi.AdaptKalshiMarket(validKalshiSeries(), raw)
		if err == nil {
			t.Fatal("expected error for empty ticker, got nil")
		}
	})

	t.Run("empty volume defaults liquidity to series total volume", func(t *testing.T) {
		raw := validKalshiMarket()
		raw.Volume = 0

		m, err := kalshi.AdaptKalshiMarket(validKalshiSeries(), raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.Liquidity != float64(validKalshiSeries().TotalVolume) {
			t.Errorf("Liquidity = %v, want %v", m.Liquidity, float64(validKalshiSeries().TotalVolume))
		}
	})
}

func TestKalshiAdapterParsesDate(t *testing.T) {
	t.Run("parses valid close_ts", func(t *testing.T) {
		raw := validKalshiMarket()
		raw.CloseTS = "2026-11-01T00:00:00Z"

		m, err := kalshi.AdaptKalshiMarket(validKalshiSeries(), raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
		if !m.ResolvesAt.Equal(want) {
			t.Errorf("ResolvesAt = %v, want %v", m.ResolvesAt, want)
		}
	})

	t.Run("falls back to expected_expiration_ts", func(t *testing.T) {
		raw := validKalshiMarket()
		raw.CloseTS = ""
		raw.ExpectedExpirationTS = "2026-11-01T00:00:00Z"

		m, err := kalshi.AdaptKalshiMarket(validKalshiSeries(), raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.ResolvesAt.IsZero() {
			t.Error("ResolvesAt should be parsed from expected_expiration_ts")
		}
	})

	t.Run("invalid close_ts returns error", func(t *testing.T) {
		raw := validKalshiMarket()
		raw.CloseTS = "not-a-date"

		_, err := kalshi.AdaptKalshiMarket(validKalshiSeries(), raw)
		if err == nil {
			t.Fatal("expected error for invalid close_ts, got nil")
		}
	})
}

func TestKalshiAdapterSetsVenue(t *testing.T) {
	m, err := kalshi.AdaptKalshiMarket(validKalshiSeries(), validKalshiMarket())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Venue != "kalshi" {
		t.Errorf("Venue = %q, want %q", m.Venue, "kalshi")
	}
	if m.VenueID != "KXTEST-26NOV01-T1" {
		t.Errorf("VenueID = %q, want %q", m.VenueID, "KXTEST-26NOV01-T1")
	}
}

func TestKalshiAdapterPreservesRawData(t *testing.T) {
	raw := validKalshiMarket()
	m, err := kalshi.AdaptKalshiMarket(validKalshiSeries(), raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.RawData == nil {
		t.Fatal("RawData is nil, want the original search payload")
	}
	preserved, ok := m.RawData.(kalshi.RawSearchMarket)
	if !ok {
		t.Fatalf("RawData type = %T, want kalshi.RawSearchMarket", m.RawData)
	}
	if preserved.Market.Ticker != raw.Ticker {
		t.Errorf("RawData.Market.Ticker = %q, want %q", preserved.Market.Ticker, raw.Ticker)
	}
}

func TestKalshiAdapterSetsCategory(t *testing.T) {
	tests := []struct {
		name         string
		seriesTicker string
		wantCategory string
	}{
		{"BTC series ticker maps to crypto", "KXBTCD", "crypto"},
		{"ETH series ticker maps to crypto", "KXETH", "crypto"},
		{"PRES series ticker maps to politics", "KXPRES", "politics"},
		{"FED series ticker maps to economics", "KXFED", "economics"},
		{"unknown series ticker maps to other", "KXWEATHER", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			series := validKalshiSeries()
			series.Category = ""
			series.SeriesTicker = tt.seriesTicker
			series.EventTicker = tt.seriesTicker + "-26"
			series.Tags = nil
			series.TopicKeywords = nil

			m, err := kalshi.AdaptKalshiMarket(series, validKalshiMarket())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Category != tt.wantCategory {
				t.Errorf("Category = %q, want %q", m.Category, tt.wantCategory)
			}
		})
	}
}

func TestKalshiAdapterCategoryFromSeries(t *testing.T) {
	series := validKalshiSeries()
	series.Category = "Economics"

	m, err := kalshi.AdaptKalshiMarket(series, validKalshiMarket())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Category != "economics" {
		t.Errorf("Category = %q, want %q", m.Category, "economics")
	}
}

func TestKalshiAdapterTitleComposition(t *testing.T) {
	t.Run("event title + subtitle produces combined title", func(t *testing.T) {
		series := validKalshiSeries()
		series.EventTitle = "Federal Reserve Interest Rate Decision"
		raw := validKalshiMarket()
		raw.YesSubtitle = ">5.25%"

		m, err := kalshi.AdaptKalshiMarket(series, raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !containsAll(m.Title, "federal reserve interest rate decision", "525") {
			t.Errorf("Title %q does not contain expected tokens", m.Title)
		}
	})

	t.Run("event title only when subtitle is empty", func(t *testing.T) {
		series := validKalshiSeries()
		series.EventTitle = "Will it rain tomorrow"
		raw := validKalshiMarket()
		raw.YesSubtitle = ""

		m, err := kalshi.AdaptKalshiMarket(series, raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !containsAll(m.Title, "will it rain tomorrow") {
			t.Errorf("Title %q does not contain event title tokens", m.Title)
		}
	})
}

func TestKalshiAdapterStatusMapping(t *testing.T) {
	tests := []struct {
		kalshiStatus string
		wantStatus   string
	}{
		{"", "open"},
		{"open", "open"},
		{"active", "open"},
		{"initialized", "pending"},
		{"inactive", "pending"},
		{"closed", "closed"},
		{"determined", "closed"},
	}

	for _, tt := range tests {
		t.Run(tt.kalshiStatus, func(t *testing.T) {
			raw := validKalshiMarket()
			raw.Status = tt.kalshiStatus

			m, err := kalshi.AdaptKalshiMarket(validKalshiSeries(), raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if m.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", m.Status, tt.wantStatus)
			}
		})
	}
}

func TestKalshiAdapterExtractsUnderlyingAndStrike(t *testing.T) {
	m, err := kalshi.AdaptKalshiMarket(validKalshiSeries(), validKalshiMarket())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Underlying != "BTC" {
		t.Errorf("Underlying = %q, want %q", m.Underlying, "BTC")
	}
	if m.StrikePrice != 80001 {
		t.Errorf("StrikePrice = %v, want 80001", m.StrikePrice)
	}
}

func TestKalshiAdapterGeneratesID(t *testing.T) {
	t.Run("same ticker produces same ID", func(t *testing.T) {
		m1, _ := kalshi.AdaptKalshiMarket(validKalshiSeries(), validKalshiMarket())
		m2, _ := kalshi.AdaptKalshiMarket(validKalshiSeries(), validKalshiMarket())
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
		raw2.Ticker = "KXOTHER-26JAN01-T1"

		m1, _ := kalshi.AdaptKalshiMarket(validKalshiSeries(), raw1)
		m2, _ := kalshi.AdaptKalshiMarket(validKalshiSeries(), raw2)
		if m1.ID == m2.ID {
			t.Error("different tickers should produce different IDs")
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
