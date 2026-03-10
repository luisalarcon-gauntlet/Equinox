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
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	anthropicModel          = "claude-sonnet-4-20250514"
	anthropicVersion        = "2023-06-01"
	maxTokens               = 1024
)

// EquivalenceResult holds the structured output from Claude's synthesis.
type EquivalenceResult struct {
	IsEquivalent bool    `json:"is_equivalent"`
	AreOpposites bool    `json:"are_opposites"`
	Confidence   float64 `json:"confidence"`
	Reasoning    string  `json:"reasoning"`
	// UsedAILayer is always true when this struct is returned from AnthropicClient.
	UsedAILayer bool
}

// AnthropicClient sends market pairs to Claude for equivalence synthesis.
// It only receives pre-computed tool results — it never calls Kalshi or Polymarket.
type AnthropicClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	log        *logger.Logger
}

// NewAnthropicClient constructs an AnthropicClient.
// baseURL is the Anthropic API root (overridable in tests via a mock server URL).
// Returns an EquinoxError if the API key is empty.
func NewAnthropicClient(cfg *config.Config, log *logger.Logger, baseURL string) (*AnthropicClient, error) {
	if cfg.AnthropicAPIKey == "" {
		return nil, &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "anthropic",
			Message: "ANTHROPIC_API_KEY environment variable not set",
		}
	}
	if baseURL == "" {
		baseURL = defaultAnthropicBaseURL
	}
	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &AnthropicClient{
		apiKey:  cfg.AnthropicAPIKey,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		log: log,
	}, nil
}

// EvaluateEquivalence sends both market titles and all tool results to Claude,
// then parses the JSON response into an EquivalenceResult.
//
// Prompt design: all tool evidence is included as structured text so Claude
// synthesises existing signals rather than reasoning from scratch. This makes
// AI decisions auditable and cost-efficient.
func (c *AnthropicClient) EvaluateEquivalence(
	ctx context.Context,
	marketA models.Market,
	marketB models.Market,
	toolResults []tools.ToolResult,
) (EquivalenceResult, error) {
	c.log.Info("ai", "anthropic", fmt.Sprintf(
		"evaluating equivalence: '%s' vs '%s'",
		marketA.Title, marketB.Title,
	))

	prompt := buildEquivalencePrompt(marketA, marketB, toolResults)
	text, err := c.complete(ctx, prompt)
	if err != nil {
		c.log.Error("ai", "anthropic", "equivalence evaluation failed", err)
		return EquivalenceResult{}, err
	}

	parsed, err := parseEquivalenceResult(text)
	if err != nil {
		c.log.Error("ai", "anthropic", "failed to parse response", err)
		return EquivalenceResult{}, err
	}

	c.log.Info("ai", "anthropic", fmt.Sprintf(
		"equivalence result: equivalent=%v opposites=%v confidence=%.2f",
		parsed.IsEquivalent, parsed.AreOpposites, parsed.Confidence,
	))

	return parsed, nil
}

// ---- Anthropic API types ----

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// complete sends a single user message to the Anthropic messages API and
// returns the raw text of the first content block.
func (c *AnthropicClient) complete(ctx context.Context, prompt string) (string, error) {
	reqBody := anthropicRequest{
		Model:     anthropicModel,
		MaxTokens: maxTokens,
		Messages: []anthropicMessage{
			{Role: "user", Content: prompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "anthropic",
			Message: "failed to marshal request body",
			Err:     err,
		}
	}

	url := c.baseURL + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "anthropic",
			Message: "failed to create HTTP request",
			Err:     err,
		}
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("content-type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "anthropic",
			Message: "HTTP request failed",
			Err:     err,
		}
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "anthropic",
			Message: "failed to read response body",
			Err:     err,
		}
	}

	if resp.StatusCode != http.StatusOK {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "anthropic",
			Message: fmt.Sprintf("unexpected HTTP status %d: %s", resp.StatusCode, string(rawBody)),
		}
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(rawBody, &apiResp); err != nil {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "anthropic",
			Message: "failed to unmarshal API response",
			Err:     err,
		}
	}

	if len(apiResp.Content) == 0 {
		return "", &equinoxerrors.EquinoxError{
			Layer:   "ai",
			Venue:   "anthropic",
			Message: "API response contained no content blocks",
		}
	}

	for _, block := range apiResp.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}

	return "", &equinoxerrors.EquinoxError{
		Layer:   "ai",
		Venue:   "anthropic",
		Message: "API response contained no text content blocks",
	}
}

// buildEquivalencePrompt constructs the synthesis prompt from market context
// and pre-computed tool results. All tool evidence is passed as structured text
// so Claude can reference explicit signals rather than free-associating.
func buildEquivalencePrompt(a, b models.Market, toolResults []tools.ToolResult) string {
	return fmt.Sprintf(`You are an equivalence detection agent for prediction markets.

Market A: "%s" (venue: %s, resolves: %s)
Market B: "%s" (venue: %s, resolves: %s)

Tool Results:
%s

Based on these tool results, determine if Market A and Market B refer to the same
real-world event or outcome. Consider that they may be:
1. Identical questions worded differently
2. Opposite sides of the same event (one YES = other NO)
3. Completely unrelated

Respond in JSON only — no markdown, no explanation outside the JSON:
{
  "is_equivalent": true/false,
  "are_opposites": true/false,
  "confidence": 0.0-1.0,
  "reasoning": "brief explanation"
}`,
		a.Title, a.Venue, formatDate(a.ResolvesAt),
		b.Title, b.Venue, formatDate(b.ResolvesAt),
		formatToolResults(toolResults),
	)
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Format("2006-01-02")
}

func formatToolResults(results []tools.ToolResult) string {
	if len(results) == 0 {
		return "  (no tool results available)"
	}
	var sb strings.Builder
	for _, r := range results {
		sb.WriteString(fmt.Sprintf("  - %s: result=%v confidence=%.2f — %s\n",
			r.ToolName, r.Result, r.Confidence, r.Reasoning))
	}
	return sb.String()
}

// parseEquivalenceResult extracts and parses the JSON equivalence result from
// Claude's response text. Claude may wrap the JSON in markdown code fences;
// we strip those before unmarshalling.
func parseEquivalenceResult(text string) (EquivalenceResult, error) {
	// Strip optional markdown code fence (```json ... ``` or ``` ... ```).
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
			Venue:   "anthropic",
			Message: fmt.Sprintf("failed to parse equivalence JSON from response: %q", text),
			Err:     err,
		}
	}

	result.UsedAILayer = true
	return result, nil
}
