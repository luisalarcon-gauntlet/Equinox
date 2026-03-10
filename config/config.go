package config

import (
	"os"
	"path/filepath"
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
//
// Kalshi key: Either KALSHI_API_KEY_PATH (file path) or KALSHI_PRIVATE_KEY
// (PEM content as env var, for Railway/cloud) must be set. KALSHI_PRIVATE_KEY
// is written to a temp file at runtime when KALSHI_API_KEY_PATH is not set.
//
// Port: SERVER_PORT is used; if unset, PORT (set by Railway and similar PaaS)
// is used; otherwise defaults to "8080".
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

	kalshiKeyPath := resolveKalshiKeyPath()
	if kalshiKeyPath == "" {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "config",
			Venue:   "kalshi",
			Message: "KALSHI_API_KEY_PATH or KALSHI_PRIVATE_KEY must be set",
		}
	}

	serverPort := envOrDefault("SERVER_PORT", "")
	if serverPort == "" {
		serverPort = envOrDefault("PORT", "8080")
	}

	cfg := &Config{
		AnthropicAPIKey:              apiKey,
		KalshiAPIKeyID:               kalshiKeyID,
		KalshiAPIKeyPath:             kalshiKeyPath,
		KalshiBaseURL:                envOrDefault("KALSHI_BASE_URL", "https://api.elections.kalshi.com/trade-api/v2"),
		PolymarketBaseURL:            envOrDefault("POLYMARKET_BASE_URL", "https://gamma-api.polymarket.com"),
		ServerPort:                   serverPort,
		HTTPTimeout:                  envDurationOrDefault("HTTP_TIMEOUT", 10*time.Second),
		HeuristicConfidenceThreshold: 0.80,
		PriceDataStalenessThreshold:  envDurationOrDefault("PRICE_STALENESS_THRESHOLD", 2*time.Minute),
	}

	return cfg, nil
}

// resolveKalshiKeyPath returns the path to the Kalshi private key file.
// Prefers KALSHI_API_KEY_PATH. If unset, uses KALSHI_PRIVATE_KEY (PEM content)
// and writes it to a temp file for cloud deployments (e.g. Railway).
func resolveKalshiKeyPath() string {
	if path := os.Getenv("KALSHI_API_KEY_PATH"); path != "" {
		return path
	}
	pemContent := os.Getenv("KALSHI_PRIVATE_KEY")
	if pemContent == "" {
		return ""
	}
	tmpDir := os.TempDir()
	tmpFile := filepath.Join(tmpDir, "equinox_kalshi_key.pem")
	if err := os.WriteFile(tmpFile, []byte(pemContent), 0600); err != nil {
		return ""
	}
	return tmpFile
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
