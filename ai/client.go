package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/equinox/config"
	equinoxerrors "github.com/equinox/errors"
	"github.com/equinox/equivalence/tools"
	"github.com/equinox/logger"
	"github.com/equinox/models"
)

const (
	defaultOpenAIBaseURL = "https://api.openai.com/v1"
	openAIModel          = "gpt-4.1-nano"
	maxTokensSingle      = 120 // sufficient for one {"is_match":…,"confidence":…,"reasoning":…}
	maxTokensBatch       = 400 // sufficient for up to 5 pair results with reasoning
)

// EquivalenceResult holds the structured output from the AI classifier.
type EquivalenceResult struct {
	IsMatch    bool    `json:"is_match"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
	// UsedAILayer is always true when this struct is returned from OpenAIClient.
	UsedAILayer bool
}

// BatchPair is one market pair with pre-computed tool signals,
// ready to be included in a multi-pair AI request.
type BatchPair struct {
	MarketA     models.Market
	MarketB     models.Market
	ToolResults []tools.ToolResult
}

// OpenAIClient sends compact market-pair classification requests to OpenAI.
// It only receives pre-computed local tool results — it never calls venue APIs.
type OpenAIClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	log        *logger.Logger
}

// NewOpenAIClient constructs an OpenAI-backed equivalence classifier.
func NewOpenAIClient(cfg *config.Config, log *logger.Logger, baseURL string) (*OpenAIClient, error) {
	if cfg.OpenAIAPIKey == "" {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "openai",
			Message: "OPENAI_API_KEY environment variable not set",
		}
	}
	if baseURL == "" {
		baseURL = cfg.OpenAIBaseURL
	}
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}
	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &OpenAIClient{
		apiKey:  cfg.OpenAIAPIKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
		log: log,
	}, nil
}

// EvaluateEquivalence sends a single market pair and its tool signals to OpenAI,
// then parses the JSON classifier response into an EquivalenceResult.
// Used by Detector.Detect for single-pair evaluation.
func (c *OpenAIClient) EvaluateEquivalence(
	ctx context.Context,
	marketA models.Market,
	marketB models.Market,
	toolResults []tools.ToolResult,
) (EquivalenceResult, error) {
	const systemPrompt = "You classify whether two prediction markets refer to the same real-world outcome. " +
		"Return JSON only with keys is_match, confidence, reasoning."

	prompt := buildEquivalencePrompt(marketA, marketB, toolResults)
	text, err := c.complete(ctx, systemPrompt, prompt, maxTokensSingle)
	if err != nil {
		c.log.Error("ai", "openai", "equivalence evaluation failed", err)
		return EquivalenceResult{}, err
	}

	parsed, err := parseSingleResult(text)
	if err != nil {
		c.log.Error("ai", "openai", "failed to parse response", err)
		return EquivalenceResult{}, err
	}

	return parsed, nil
}

// EvaluateBatch sends up to 5 market pairs in a single OpenAI request and
// returns one EquivalenceResult per pair in the same order.
// The response is validated: if the model returns a different number of results
// than pairs sent, an error is returned so the caller can fall back gracefully.
func (c *OpenAIClient) EvaluateBatch(ctx context.Context, pairs []BatchPair) ([]EquivalenceResult, error) {
	if len(pairs) == 0 {
		return nil, nil
	}

	const systemPrompt = "You classify prediction market pairs. " +
		"Return a JSON array with exactly one object per pair in order. " +
		`Each object must have: "pair" (integer index), "is_match" (boolean), ` +
		`"confidence" (0.0-1.0), "reasoning" (brief string). ` +
		"Return the JSON array only — no other text."

	prompt := buildBatchPrompt(pairs)
	text, err := c.complete(ctx, systemPrompt, prompt, maxTokensBatch)
	if err != nil {
		c.log.Error("ai", "openai", "batch evaluation failed", err)
		return nil, err
	}

	results, err := parseBatchResults(text, len(pairs))
	if err != nil {
		c.log.Error("ai", "openai", "failed to parse batch response", err)
		return nil, err
	}

	return results, nil
}

// ── HTTP transport ────────────────────────────────────────────────────────────

type openAIChatCompletionRequest struct {
	Model       string                      `json:"model"`
	MaxTokens   int                         `json:"max_tokens"`
	Temperature float64                     `json:"temperature"`
	Messages    []openAIChatCompletionEntry `json:"messages"`
}

type openAIChatCompletionEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *OpenAIClient) complete(ctx context.Context, systemPrompt, userPrompt string, maxTok int) (string, error) {
	reqBody := openAIChatCompletionRequest{
		Model:       openAIModel,
		MaxTokens:   maxTok,
		Temperature: 0,
		Messages: []openAIChatCompletionEntry{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "openai",
			Message: "failed to marshal request body",
			Err:     err,
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "openai",
			Message: "failed to create HTTP request",
			Err:     err,
		}
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "openai",
			Message: "HTTP request failed",
			Err:     err,
		}
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "openai",
			Message: "failed to read response body",
			Err:     err,
		}
	}

	if resp.StatusCode != http.StatusOK {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "openai",
			Message: fmt.Sprintf("unexpected HTTP status %d: %s", resp.StatusCode, string(rawBody)),
		}
	}

	var apiResp openAIChatCompletionResponse
	if err := json.Unmarshal(rawBody, &apiResp); err != nil {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "openai",
			Message: "failed to unmarshal API response",
			Err:     err,
		}
	}

	if len(apiResp.Choices) == 0 {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "openai",
			Message: "API response contained no choices",
		}
	}

	content := strings.TrimSpace(apiResp.Choices[0].Message.Content)
	if content == "" {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "openai",
			Message: "API response contained empty message content",
		}
	}
	return content, nil
}

// ── Prompt builders ───────────────────────────────────────────────────────────

// buildEquivalencePrompt produces the single-pair classification prompt.
func buildEquivalencePrompt(a, b models.Market, toolResults []tools.ToolResult) string {
	return fmt.Sprintf(
		`Decide if these two prediction markets resolve to the same real-world outcome.

Market A: title=%q venue=%s resolves=%s
Market B: title=%q venue=%s resolves=%s
Signals:
%s

Return JSON only:
{"is_match":true|false,"confidence":0.0-1.0,"reasoning":"short explanation"}`,
		a.Title, a.Venue, formatDate(a.ResolvesAt),
		b.Title, b.Venue, formatDate(b.ResolvesAt),
		formatToolResults(toolResults),
	)
}

// buildBatchPrompt produces the multi-pair classification prompt.
// Each pair is numbered [0]…[N-1]; the model must return a JSON array
// with exactly N objects in the same order.
func buildBatchPrompt(pairs []BatchPair) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Classify these %d prediction market pairs:\n\n", len(pairs))

	for i, p := range pairs {
		fmt.Fprintf(&sb, "[%d]\n", i)
		fmt.Fprintf(&sb, "A: title=%q venue=%s resolves=%s\n",
			p.MarketA.Title, p.MarketA.Venue, formatDate(p.MarketA.ResolvesAt))
		fmt.Fprintf(&sb, "B: title=%q venue=%s resolves=%s\n",
			p.MarketB.Title, p.MarketB.Venue, formatDate(p.MarketB.ResolvesAt))
		if len(p.ToolResults) > 0 {
			sb.WriteString("Signals:\n")
			sb.WriteString(formatToolResults(p.ToolResults))
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	fmt.Fprintf(&sb,
		"Return a JSON array of exactly %d objects in order.\n"+
			`Each: {"pair":<index>,"is_match":true|false,"confidence":0.0-1.0,"reasoning":"brief"}`,
		len(pairs),
	)
	return sb.String()
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Format("2006-01-02")
}

func formatToolResults(results []tools.ToolResult) string {
	if len(results) == 0 {
		return "- none"
	}
	var sb strings.Builder
	for _, r := range results {
		sb.WriteString(fmt.Sprintf("- %s result=%t confidence=%.2f\n",
			r.ToolName, r.Result, r.Confidence))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// ── Response parsers ──────────────────────────────────────────────────────────

// parseSingleResult extracts an EquivalenceResult from the model's plain-JSON reply.
func parseSingleResult(text string) (EquivalenceResult, error) {
	cleaned := strings.TrimSpace(text)
	if idx := strings.Index(cleaned, "{"); idx > 0 {
		cleaned = cleaned[idx:]
	}
	if idx := strings.LastIndex(cleaned, "}"); idx >= 0 && idx < len(cleaned)-1 {
		cleaned = cleaned[:idx+1]
	}

	var result EquivalenceResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return EquivalenceResult{}, &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "openai",
			Message: fmt.Sprintf("failed to parse equivalence JSON from response: %q", text),
			Err:     err,
		}
	}

	result.UsedAILayer = true
	return result, nil
}

// batchItem is the per-pair structure expected in the model's array response.
type batchItem struct {
	Pair       int     `json:"pair"`
	IsMatch    bool    `json:"is_match"`
	Confidence float64 `json:"confidence"`
	Reasoning  string  `json:"reasoning"`
}

// parseBatchResults decodes the model's JSON array response and validates that
// it contains exactly expectedLen items with in-range pair indices.
// Items may be returned out of order; they are re-indexed by their pair field.
func parseBatchResults(text string, expectedLen int) ([]EquivalenceResult, error) {
	cleaned := strings.TrimSpace(text)

	// Strip any leading text before the opening bracket (e.g. markdown fences).
	if idx := strings.Index(cleaned, "["); idx > 0 {
		cleaned = cleaned[idx:]
	}
	if idx := strings.LastIndex(cleaned, "]"); idx >= 0 && idx < len(cleaned)-1 {
		cleaned = cleaned[:idx+1]
	}

	var items []batchItem
	if err := json.Unmarshal([]byte(cleaned), &items); err != nil {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "openai",
			Message: fmt.Sprintf("failed to parse batch JSON from response: %q", text),
			Err:     err,
		}
	}

	if len(items) != expectedLen {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "openai",
			Message: fmt.Sprintf("batch response has %d items, expected %d", len(items), expectedLen),
		}
	}

	results := make([]EquivalenceResult, expectedLen)
	for _, item := range items {
		if item.Pair < 0 || item.Pair >= expectedLen {
			return nil, &equinoxerrors.EquinoxError{
				Layer:   "ai",
				Venue:   "openai",
				Message: fmt.Sprintf("batch response contains out-of-range pair index %d (valid range 0-%d)", item.Pair, expectedLen-1),
			}
		}
		results[item.Pair] = EquivalenceResult{
			IsMatch:     item.IsMatch,
			Confidence:  item.Confidence,
			Reasoning:   item.Reasoning,
			UsedAILayer: true,
		}
	}

	return results, nil
}
