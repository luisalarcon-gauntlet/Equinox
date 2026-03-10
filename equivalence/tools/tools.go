package tools

import "github.com/equinox/models"

// ToolResult holds the outcome of a single equivalence check tool.
// Every check_*.go file produces one of these.
type ToolResult struct {
	ToolName   string
	Result     bool    // true = evidence of equivalence (or opposite-hood) found
	Confidence float64 // 0.0 to 1.0
	Reasoning  string  // human-readable explanation passed to Claude
}

// Tool is the interface implemented by every equivalence check tool.
// The AI layer calls Execute on each tool and passes the results to Claude.
type Tool interface {
	Name() string
	Description() string
	Execute(marketA, marketB models.Market) (ToolResult, error)
}
