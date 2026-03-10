package config_test

import (
	"testing"
	"time"

	"github.com/equinox/config"
)

// setEnv sets environment variables for the duration of a test and restores
// them via t.Cleanup.
func setEnv(t *testing.T, pairs map[string]string) {
	t.Helper()
	for k, v := range pairs {
		t.Setenv(k, v)
	}
}

// minimalEnv returns the minimum set of environment variables required for
// config.Load() to succeed. Tests that only care about one specific field
// start from this base and add or override individual entries.
func minimalEnv() map[string]string {
	return map[string]string{
		"ANTHROPIC_API_KEY":   "k",
		"KALSHI_API_KEY_ID":   "test-key-id",
		"KALSHI_API_KEY_PATH": "/path/to/key.pem",
	}
}

func TestLoadReturnsErrorWhenAPIKeyMissing(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	_, err := config.Load()
	if err == nil {
		t.Error("Load() = nil error, want error when ANTHROPIC_API_KEY not set")
	}
}

func TestLoadReturnsErrorWhenKalshiAPIKeyIDMissing(t *testing.T) {
	env := minimalEnv()
	env["KALSHI_API_KEY_ID"] = ""
	setEnv(t, env)

	_, err := config.Load()
	if err == nil {
		t.Error("Load() = nil error, want error when KALSHI_API_KEY_ID not set")
	}
}

func TestLoadReturnsErrorWhenKalshiAPIKeyPathMissing(t *testing.T) {
	env := minimalEnv()
	env["KALSHI_API_KEY_PATH"] = ""
	setEnv(t, env)

	_, err := config.Load()
	if err == nil {
		t.Error("Load() = nil error, want error when KALSHI_API_KEY_PATH not set")
	}
}

func TestLoadSucceedsWithAPIKeySet(t *testing.T) {
	env := minimalEnv()
	env["ANTHROPIC_API_KEY"] = "test-key-123"
	setEnv(t, env)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.AnthropicAPIKey != "test-key-123" {
		t.Errorf("AnthropicAPIKey = %q, want %q", cfg.AnthropicAPIKey, "test-key-123")
	}
}

func TestLoadStoresKalshiAPIKeyID(t *testing.T) {
	env := minimalEnv()
	env["KALSHI_API_KEY_ID"] = "my-kalshi-key-id"
	setEnv(t, env)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.KalshiAPIKeyID != "my-kalshi-key-id" {
		t.Errorf("KalshiAPIKeyID = %q, want %q", cfg.KalshiAPIKeyID, "my-kalshi-key-id")
	}
}

func TestLoadStoresKalshiAPIKeyPath(t *testing.T) {
	env := minimalEnv()
	env["KALSHI_API_KEY_PATH"] = "/secrets/kalshi.pem"
	setEnv(t, env)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.KalshiAPIKeyPath != "/secrets/kalshi.pem" {
		t.Errorf("KalshiAPIKeyPath = %q, want %q", cfg.KalshiAPIKeyPath, "/secrets/kalshi.pem")
	}
}

func TestLoadDefaultKalshiBaseURL(t *testing.T) {
	setEnv(t, minimalEnv())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := "https://trading-api.kalshi.com/trade-api/v2"
	if cfg.KalshiBaseURL != want {
		t.Errorf("KalshiBaseURL = %q, want %q", cfg.KalshiBaseURL, want)
	}
}

func TestLoadDefaultPolymarketBaseURL(t *testing.T) {
	setEnv(t, minimalEnv())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := "https://gamma-api.polymarket.com"
	if cfg.PolymarketBaseURL != want {
		t.Errorf("PolymarketBaseURL = %q, want %q", cfg.PolymarketBaseURL, want)
	}
}

func TestLoadDefaultHTTPTimeout(t *testing.T) {
	setEnv(t, minimalEnv())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPTimeout != 10*time.Second {
		t.Errorf("HTTPTimeout = %v, want 10s", cfg.HTTPTimeout)
	}
}

func TestLoadDefaultServerPort(t *testing.T) {
	setEnv(t, minimalEnv())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ServerPort != "8080" {
		t.Errorf("ServerPort = %q, want %q", cfg.ServerPort, "8080")
	}
}

func TestLoadDefaultHeuristicThreshold(t *testing.T) {
	setEnv(t, minimalEnv())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HeuristicConfidenceThreshold != 0.80 {
		t.Errorf("HeuristicConfidenceThreshold = %v, want 0.80", cfg.HeuristicConfidenceThreshold)
	}
}

func TestLoadDefaultStalenessThreshold(t *testing.T) {
	setEnv(t, minimalEnv())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.PriceDataStalenessThreshold != 2*time.Minute {
		t.Errorf("PriceDataStalenessThreshold = %v, want 2m", cfg.PriceDataStalenessThreshold)
	}
}

func TestLoadOverridesKalshiBaseURL(t *testing.T) {
	env := minimalEnv()
	env["KALSHI_BASE_URL"] = "https://demo.kalshi.co/trade-api/v2"
	setEnv(t, env)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := "https://demo.kalshi.co/trade-api/v2"
	if cfg.KalshiBaseURL != want {
		t.Errorf("KalshiBaseURL = %q, want %q", cfg.KalshiBaseURL, want)
	}
}

func TestLoadOverridesPolymarketBaseURL(t *testing.T) {
	env := minimalEnv()
	env["POLYMARKET_BASE_URL"] = "https://custom.polymarket.com"
	setEnv(t, env)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := "https://custom.polymarket.com"
	if cfg.PolymarketBaseURL != want {
		t.Errorf("PolymarketBaseURL = %q, want %q", cfg.PolymarketBaseURL, want)
	}
}

func TestLoadOverridesServerPort(t *testing.T) {
	env := minimalEnv()
	env["SERVER_PORT"] = "9090"
	setEnv(t, env)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ServerPort != "9090" {
		t.Errorf("ServerPort = %q, want %q", cfg.ServerPort, "9090")
	}
}

func TestLoadOverridesHTTPTimeout(t *testing.T) {
	env := minimalEnv()
	env["HTTP_TIMEOUT"] = "30s"
	setEnv(t, env)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.HTTPTimeout != 30*time.Second {
		t.Errorf("HTTPTimeout = %v, want 30s", cfg.HTTPTimeout)
	}
}

func TestLoadErrorWrapsAsEquinoxError(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// The error message must contain the config layer indicator.
	errStr := err.Error()
	if len(errStr) == 0 {
		t.Error("error string is empty")
	}
	// Must contain [config] as per EquinoxError format.
	if !contains(errStr, "[config]") {
		t.Errorf("error %q does not contain [config]", errStr)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
