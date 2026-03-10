package config

import (
	"os"
	"time"

	equinoxerrors "github.com/equinox/errors"
)

// Config holds all runtime configuration for Equinox.
// Every field is loaded from environment variables; sensible defaults are
// applied for every optional field so the binary works out of the box
// with only ANTHROPIC_API_KEY set.
type Config struct {
	AnthropicAPIKey              string
	KalshiAPIKeyID               string
	KalshiAPIKeyPath             string
	KalshiBaseURL                string
	PolymarketBaseURL            string
	HTTPTimeout                  time.Duration
	ServerPort                   string
	HeuristicConfidenceThreshold float64
	PriceDataStalenessThreshold  time.Duration
}

// Load reads configuration from environment variables and applies defaults.
// It returns an EquinoxError (layer "config") if any required variable is absent.
func Load() (*Config, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "config",
			Venue:   "",
			Message: "ANTHROPIC_API_KEY not set",
		}
	}

	kalshiKeyID := os.Getenv("KALSHI_API_KEY_ID")
	if kalshiKeyID == "" {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "config",
			Venue:   "kalshi",
			Message: "KALSHI_API_KEY_ID not set",
		}
	}

	kalshiKeyPath := os.Getenv("KALSHI_API_KEY_PATH")
	if kalshiKeyPath == "" {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "config",
			Venue:   "kalshi",
			Message: "KALSHI_API_KEY_PATH not set",
		}
	}

	cfg := &Config{
		AnthropicAPIKey:              apiKey,
		KalshiAPIKeyID:               kalshiKeyID,
		KalshiAPIKeyPath:             kalshiKeyPath,
		KalshiBaseURL:                envOrDefault("KALSHI_BASE_URL", "https://api.elections.kalshi.com/trade-api/v2"),
		PolymarketBaseURL:            envOrDefault("POLYMARKET_BASE_URL", "https://gamma-api.polymarket.com"),
		ServerPort:                   envOrDefault("SERVER_PORT", "8080"),
		HTTPTimeout:                  envDurationOrDefault("HTTP_TIMEOUT", 10*time.Second),
		HeuristicConfidenceThreshold: 0.80,
		PriceDataStalenessThreshold:  envDurationOrDefault("PRICE_STALENESS_THRESHOLD", 2*time.Minute),
	}

	return cfg, nil
}

func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func envDurationOrDefault(key string, defaultVal time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return defaultVal
	}
	return d
}
