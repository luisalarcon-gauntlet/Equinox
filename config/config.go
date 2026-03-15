package config

import (
	"os"
	"time"

	equinoxerrors "github.com/equinox/errors"
)

// Config holds all runtime configuration for Equinox.
// Every field is loaded from environment variables; sensible defaults are
// applied for every optional field so the binary works out of the box
// with only OPENAI_API_KEY set.
type Config struct {
	OpenAIAPIKey                 string
	OpenAIBaseURL                string
	KalshiBaseURL                string
	KalshiDBBaseURL              string
	KalshiDBAPIKey               string
	PolymarketBaseURL            string
	HTTPTimeout                  time.Duration
	ServerPort                   string
	HeuristicConfidenceThreshold float64
	PriceDataStalenessThreshold  time.Duration
}

// Load reads configuration from environment variables and applies defaults.
// It returns an EquinoxError (layer "config") if any required variable is absent.
//
// Port: SERVER_PORT is used; if unset, PORT (set by Railway and similar PaaS)
// is used; otherwise defaults to "8080".
func Load() (*Config, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "config",
			Venue:   "",
			Message: "OPENAI_API_KEY not set",
		}
	}

	serverPort := envOrDefault("SERVER_PORT", "")
	if serverPort == "" {
		serverPort = envOrDefault("PORT", "8080")
	}

	cfg := &Config{
		OpenAIAPIKey:                 apiKey,
		OpenAIBaseURL:                envOrDefault("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		KalshiBaseURL:                envOrDefault("KALSHI_BASE_URL", "https://api.elections.kalshi.com"),
		KalshiDBBaseURL:              envOrDefault("KALSHI_DB_BASE_URL", "http://localhost:8000"),
		KalshiDBAPIKey:               os.Getenv("KALSHI_DB_API_KEY"),
		PolymarketBaseURL:            envOrDefault("POLYMARKET_BASE_URL", "https://gamma-api.polymarket.com"),
		ServerPort:                   serverPort,
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
