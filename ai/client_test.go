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

type openAIResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func mockOpenAIServer(t *testing.T, statusCode int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(body))
	}))
}

func validOpenAIBody(text string) string {
	resp := openAIResp{}
	resp.Choices = []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}{{Message: struct {
		Content string `json:"content"`
	}{Content: text}}}
	b, _ := json.Marshal(resp)
	return string(b)
}

func testClient(t *testing.T, serverURL string) *ai.OpenAIClient {
	t.Helper()
	cfg := &config.Config{
		OpenAIAPIKey: "test-key",
		HTTPTimeout:  5 * time.Second,
	}
	log := logger.New(io.Discard)
	client, err := ai.NewOpenAIClient(cfg, log, serverURL)
	if err != nil {
		t.Fatalf("NewOpenAIClient() error = %v", err)
	}
	return client
}

func testMarkets() (models.Market, models.Market) {
	future := time.Now().Add(180 * 24 * time.Hour)
	marketA := models.Market{
		ID:         "a",
		Venue:      "kalshi",
		Title:      "will democrats control the house after 2026 midterms",
		ResolvesAt: future,
		FetchedAt:  time.Now(),
	}
	marketB := models.Market{
		ID:         "b",
		Venue:      "polymarket",
		Title:      "will democrats win house majority 2026",
		ResolvesAt: future,
		FetchedAt:  time.Now(),
	}
	return marketA, marketB
}

func TestOpenAIClientReturnsEquivalenceResult(t *testing.T) {
	jsonResp := `{"is_match":true,"confidence":0.93,"reasoning":"same election question"}`
	srv := mockOpenAIServer(t, http.StatusOK, validOpenAIBody(jsonResp))
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	toolResults := []tools.ToolResult{
		{ToolName: "check_entity_match", Result: true, Confidence: 0.90, Reasoning: "entity overlap"},
		{ToolName: "check_date_alignment", Result: true, Confidence: 1.0, Reasoning: "same date"},
	}

	result, err := client.EvaluateEquivalence(context.Background(), marketA, marketB, toolResults)
	if err != nil {
		t.Fatalf("EvaluateEquivalence() error = %v", err)
	}
	if !result.IsMatch {
		t.Errorf("IsMatch = false, want true")
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

func TestOpenAIClientHandlesNon200(t *testing.T) {
	srv := mockOpenAIServer(t, http.StatusInternalServerError, `{"error":{"message":"internal server error"}}`)
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	_, err := client.EvaluateEquivalence(context.Background(), marketA, marketB, nil)
	if err == nil {
		t.Errorf("EvaluateEquivalence() error = nil, want error for HTTP 500")
	}
}

func TestOpenAIClientHandlesMalformedJSON(t *testing.T) {
	srv := mockOpenAIServer(t, http.StatusOK, validOpenAIBody("not-valid-json"))
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	_, err := client.EvaluateEquivalence(context.Background(), marketA, marketB, nil)
	if err == nil {
		t.Errorf("EvaluateEquivalence() error = nil, want error for malformed JSON in response text")
	}
}

func TestOpenAIClientHandlesTimeout(t *testing.T) {
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
		OpenAIAPIKey: "test-key",
		HTTPTimeout:  5 * time.Second,
	}
	log := logger.New(io.Discard)
	client, _ := ai.NewOpenAIClient(cfg, log, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	marketA, marketB := testMarkets()
	_, err := client.EvaluateEquivalence(ctx, marketA, marketB, nil)
	if err == nil {
		t.Errorf("EvaluateEquivalence() error = nil, want timeout error")
	}
}

func TestOpenAIClientRejectsEmptyAPIKey(t *testing.T) {
	cfg := &config.Config{
		OpenAIAPIKey: "",
		HTTPTimeout:  5 * time.Second,
	}
	log := logger.New(io.Discard)
	_, err := ai.NewOpenAIClient(cfg, log, "http://localhost")
	if err == nil {
		t.Errorf("NewOpenAIClient() error = nil, want error for empty API key")
	}
}

func TestOpenAIClientGracefulFallback(t *testing.T) {
	srv := mockOpenAIServer(t, http.StatusOK, `{"choices":[]}`)
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	_, err := client.EvaluateEquivalence(context.Background(), marketA, marketB, nil)
	if err == nil {
		t.Errorf("EvaluateEquivalence() error = nil, want error for empty choices array")
	}
}

// ── EvaluateBatch ─────────────────────────────────────────────────────────────

func TestOpenAIClientBatchEvaluateReturnsTwoResults(t *testing.T) {
	// Model returns a 2-element array in order.
	batchResp := `[{"pair":0,"is_match":true,"confidence":0.93,"reasoning":"same event"},` +
		`{"pair":1,"is_match":false,"confidence":0.12,"reasoning":"different event"}]`
	srv := mockOpenAIServer(t, http.StatusOK, validOpenAIBody(batchResp))
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	pairs := []ai.BatchPair{
		{MarketA: marketA, MarketB: marketB},
		{MarketA: marketB, MarketB: marketA},
	}

	results, err := client.EvaluateBatch(context.Background(), pairs)
	if err != nil {
		t.Fatalf("EvaluateBatch() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !results[0].IsMatch {
		t.Errorf("pair 0: IsMatch = false, want true")
	}
	if results[1].IsMatch {
		t.Errorf("pair 1: IsMatch = true, want false")
	}
	if !results[0].UsedAILayer {
		t.Errorf("pair 0: UsedAILayer = false, want true")
	}
}

func TestOpenAIClientBatchEvaluateHandlesOutOfOrderResponse(t *testing.T) {
	// Model returns items in reverse order; parseBatchResults must re-index by pair field.
	batchResp := `[{"pair":1,"is_match":false,"confidence":0.10,"reasoning":"b"},` +
		`{"pair":0,"is_match":true,"confidence":0.95,"reasoning":"a"}]`
	srv := mockOpenAIServer(t, http.StatusOK, validOpenAIBody(batchResp))
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	pairs := []ai.BatchPair{
		{MarketA: marketA, MarketB: marketB},
		{MarketA: marketB, MarketB: marketA},
	}

	results, err := client.EvaluateBatch(context.Background(), pairs)
	if err != nil {
		t.Fatalf("EvaluateBatch() error = %v", err)
	}
	if !results[0].IsMatch {
		t.Errorf("pair 0: IsMatch = false, want true (re-indexed from pair field)")
	}
	if results[1].IsMatch {
		t.Errorf("pair 1: IsMatch = true, want false (re-indexed from pair field)")
	}
}

func TestOpenAIClientBatchLengthMismatchReturnsError(t *testing.T) {
	// Response has 1 item but we sent 2 pairs → must error so caller can fall back.
	batchResp := `[{"pair":0,"is_match":true,"confidence":0.93,"reasoning":"x"}]`
	srv := mockOpenAIServer(t, http.StatusOK, validOpenAIBody(batchResp))
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	pairs := []ai.BatchPair{
		{MarketA: marketA, MarketB: marketB},
		{MarketA: marketB, MarketB: marketA},
	}

	_, err := client.EvaluateBatch(context.Background(), pairs)
	if err == nil {
		t.Errorf("EvaluateBatch() error = nil, want error for length mismatch")
	}
}

func TestOpenAIClientBatchOutOfRangeIndexReturnsError(t *testing.T) {
	// Model returns a pair index that is out of range.
	batchResp := `[{"pair":99,"is_match":true,"confidence":0.90,"reasoning":"x"}]`
	srv := mockOpenAIServer(t, http.StatusOK, validOpenAIBody(batchResp))
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	pairs := []ai.BatchPair{{MarketA: marketA, MarketB: marketB}}

	_, err := client.EvaluateBatch(context.Background(), pairs)
	if err == nil {
		t.Errorf("EvaluateBatch() error = nil, want error for out-of-range pair index")
	}
}

func TestOpenAIClientBatchHandlesMalformedJSON(t *testing.T) {
	srv := mockOpenAIServer(t, http.StatusOK, validOpenAIBody("not-valid-json"))
	defer srv.Close()

	client := testClient(t, srv.URL)
	marketA, marketB := testMarkets()

	pairs := []ai.BatchPair{{MarketA: marketA, MarketB: marketB}}

	_, err := client.EvaluateBatch(context.Background(), pairs)
	if err == nil {
		t.Errorf("EvaluateBatch() error = nil, want error for malformed JSON")
	}
}

func TestOpenAIClientBatchEmptyInputReturnsNil(t *testing.T) {
	// No HTTP call should be made for an empty input slice.
	client := testClient(t, "http://localhost:0") // unreachable — must not be contacted

	results, err := client.EvaluateBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("EvaluateBatch() error = %v, want nil for empty input", err)
	}
	if results != nil {
		t.Errorf("EvaluateBatch() results = %v, want nil for empty input", results)
	}
}
