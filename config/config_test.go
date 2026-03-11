package config_test

import (
	"testing"
	"time"

	"github.com/equinox/config"
)

func setEnv(t *testing.T, pairs map[string]string) {
	t.Helper()
	for k, v := range pairs {
		t.Setenv(k, v)
	}
}

func minimalEnv() map[string]string {
	return map[string]string{
		"OPENAI_API_KEY": "k",
	}
}

func TestLoadReturnsErrorWhenAPIKeyMissing(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	_, err := config.Load()
	if err == nil {
		t.Error("Load() = nil error, want error when OPENAI_API_KEY not set")
	}
}

func TestLoadSucceedsWithoutKalshiCredentials(t *testing.T) {
	setEnv(t, minimalEnv())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.KalshiBaseURL == "" {
		t.Error("KalshiBaseURL should be populated by default")
	}
}

func TestLoadStoresOpenAIAPIKey(t *testing.T) {
	env := minimalEnv()
	env["OPENAI_API_KEY"] = "test-key-123"
	setEnv(t, env)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.OpenAIAPIKey != "test-key-123" {
		t.Errorf("OpenAIAPIKey = %q, want %q", cfg.OpenAIAPIKey, "test-key-123")
	}
}

func TestLoadDefaultKalshiBaseURL(t *testing.T) {
	setEnv(t, minimalEnv())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := "https://api.elections.kalshi.com"
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
	env["KALSHI_BASE_URL"] = "https://demo.kalshi.co"
	setEnv(t, env)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := "https://demo.kalshi.co"
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

func TestLoadUsesPortWhenServerPortNotSet(t *testing.T) {
	env := minimalEnv()
	env["SERVER_PORT"] = ""
	env["PORT"] = "3000"
	setEnv(t, env)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ServerPort != "3000" {
		t.Errorf("ServerPort = %q, want %q (from PORT)", cfg.ServerPort, "3000")
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
	t.Setenv("OPENAI_API_KEY", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	errStr := err.Error()
	if len(errStr) == 0 {
		t.Error("error string is empty")
	}
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
