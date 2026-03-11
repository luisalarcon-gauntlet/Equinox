package equivalence

import (
	"context"
	"sync/atomic"
)

type aiStatsContextKey struct{}

// AIStatsCollector tracks how many AI calls were attempted during a workflow.
type AIStatsCollector struct {
	attempts atomic.Int64
}

// NewAIStatsCollector creates a per-workflow AI call counter.
func NewAIStatsCollector() *AIStatsCollector {
	return &AIStatsCollector{}
}

// Count returns the number of attempted AI calls recorded so far.
func (c *AIStatsCollector) Count() int64 {
	if c == nil {
		return 0
	}
	return c.attempts.Load()
}

// WithAIStatsCollector attaches a collector to ctx so downstream code can
// record AI usage without changing method signatures.
func WithAIStatsCollector(ctx context.Context, collector *AIStatsCollector) context.Context {
	if collector == nil {
		return ctx
	}
	return context.WithValue(ctx, aiStatsContextKey{}, collector)
}

func recordAIAttempt(ctx context.Context) {
	if ctx == nil {
		return
	}
	collector, _ := ctx.Value(aiStatsContextKey{}).(*AIStatsCollector)
	if collector != nil {
		collector.attempts.Add(1)
	}
}
