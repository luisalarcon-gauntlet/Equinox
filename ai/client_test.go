package ai_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/equinox/ai"
	"github.com/equinox/config"
	"github.com/equinox/equivalence/tools"
	"github.com/equinox/logger"
	"github.com/equinox/models"
)

// anthropicResp is a minimal Anthropic messages API response.
type anthropicResp struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

// mockAnthropicServer starts a test HTTP server that returns canned responses.
func mockAnthropicServer(t *testing.T, statusCode int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(body))
	}))
}

// validAnthropicBody builds a minimal valid Anthropic response with the given JSON text.
func validAnthropicBody(text string) string {
	resp := anthropicResp{}
	resp.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: text}}
	b, _ := json.Marshal(resp)
	return string(b)
}

func testClient(t *testing.T, serverURL string) *ai.AnthropicClient {
	t.Helper()
	cfg := &config.Config{
		AnthropicAPIKey: "test-key",
		HTTPTimeout:     5 * time.Second,
	}
	log := logger.New(io.Discard) // discard logs in tests
	client, err := ai.NewAnthropicClient(cfg, log, serverURL)
	if err != nil {
		t.Fatalf("NewAnthropicClient() error = %v", err)
	}
	return client
}

func testMarkets() (models.Market, models.Market) {
	future := time.Now().Add(180 * 24 * time.Hour)
	marketA := models.Market{
		ID:         "a",
		Venue:      "kalshi",
		Title:      "will gop control the house after 2026 midterms",
		ResolvesAt: future,
		FetchedAt:  time.Now(),
	}
	marketB := models.Market{
		ID:         "b",
		Venue:      "polymarket",
		Title:      "democrats win house majority 2026",
		ResolvesAt: future,
		FetchedAt:  time.Now(),
	}
	return marketA, marketB
}

func TestAnthropicClientReturnsEquivalenceResult(t *testing.T) {
	jsonResp := `{"is_equivalent":true,"are_opposites":true,"confidence":0.93,"reasoning":"These are opposite sides of the 2026 House control question."}`
	srv := mockAnthropicServer(t, http.StatusOK, validAnthropicBody(jsonResp))
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	toolResults := []tools.ToolResult{
		{ToolName: "check_opposites", Result: true, Confidence: 0.90, Reasoning: "party opposites detected"},
		{ToolName: "check_date_alignment", Result: true, Confidence: 1.0, Reasoning: "same date"},
	}

	result, err := client.EvaluateEquivalence(context.Background(), marketA, marketB, toolResults)
	if err != nil {
		t.Fatalf("EvaluateEquivalence() error = %v", err)
	}
	if !result.IsEquivalent {
		t.Errorf("IsEquivalent = false, want true")
	}
	if !result.AreOpposites {
		t.Errorf("AreOpposites = false, want true")
	}
	if result.Confidence < 0.90 {
		t.Errorf("Confidence = %v, want >= 0.90", result.Confidence)
	}
	if result.Reasoning == "" {
		t.Errorf("Reasoning must not be empty")
	}
	if !result.UsedAILayer {
		t.Errorf("UsedAILayer = false, want true")
	}
}

func TestAnthropicClientHandlesNon200(t *testing.T) {
	srv := mockAnthropicServer(t, http.StatusInternalServerError, `{"error":"internal server error"}`)
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	_, err := client.EvaluateEquivalence(context.Background(), marketA, marketB, nil)
	if err == nil {
		t.Errorf("EvaluateEquivalence() error = nil, want error for HTTP 500")
	}
}

func TestAnthropicClientHandlesMalformedJSON(t *testing.T) {
	// The Anthropic response is valid HTTP 200 but the text content is not valid JSON.
	srv := mockAnthropicServer(t, http.StatusOK, validAnthropicBody("not-valid-json"))
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	_, err := client.EvaluateEquivalence(context.Background(), marketA, marketB, nil)
	if err == nil {
		t.Errorf("EvaluateEquivalence() error = nil, want error for malformed JSON in response text")
	}
}

func TestAnthropicClientHandlesTimeout(t *testing.T) {
	// Server that blocks briefly — longer than the context deadline.
	// We use a channel to unblock the handler when the test is done so
	// srv.Close() does not hang waiting for the goroutine to exit.
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-unblock:
		}
	}))
	defer func() {
		close(unblock)
		srv.CloseClientConnections()
		srv.Close()
	}()

	cfg := &config.Config{
		AnthropicAPIKey: "test-key",
		HTTPTimeout:     5 * time.Second,
	}
	log := logger.New(io.Discard)
	client, _ := ai.NewAnthropicClient(cfg, log, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	marketA, marketB := testMarkets()
	_, err := client.EvaluateEquivalence(ctx, marketA, marketB, nil)
	if err == nil {
		t.Errorf("EvaluateEquivalence() error = nil, want timeout error")
	}
}

func TestAnthropicClientRejectsEmptyAPIKey(t *testing.T) {
	cfg := &config.Config{
		AnthropicAPIKey: "",
		HTTPTimeout:     5 * time.Second,
	}
	log := logger.New(io.Discard)
	_, err := ai.NewAnthropicClient(cfg, log, "http://localhost")
	if err == nil {
		t.Errorf("NewAnthropicClient() error = nil, want error for empty API key")
	}
}

func TestAnthropicClientGracefulFallback(t *testing.T) {
	// Server that returns empty content array — malformed but no panic expected.
	srv := mockAnthropicServer(t, http.StatusOK, `{"content":[]}`)
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	_, err := client.EvaluateEquivalence(context.Background(), marketA, marketB, nil)
	if err == nil {
		t.Errorf("EvaluateEquivalence() error = nil, want error for empty content array")
	}
}
