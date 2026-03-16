// Package trace provides a lightweight per-query step collector that is
// injected via context. It mirrors the equivalence.AIStatsCollector pattern:
// zero imports, mutex-protected, nil-safe via FromContext.
package trace

import (
	"context"
	"sync"
)

type contextKey struct{}

// QueryTrace collects an ordered log of steps taken during a single query.
// All methods are safe for concurrent use.
type QueryTrace struct {
	mu    sync.Mutex
	steps []string
}

// Add appends step to the trace. No-op on a nil receiver so callers do not
// need to guard against a missing trace in tests or non-server paths.
func (t *QueryTrace) Add(step string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.steps = append(t.steps, step)
	t.mu.Unlock()
}

// Steps returns a snapshot of the accumulated steps in insertion order.
func (t *QueryTrace) Steps() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	out := make([]string, len(t.steps))
	copy(out, t.steps)
	t.mu.Unlock()
	return out
}

// NewContext creates a new QueryTrace, stores it in ctx, and returns both.
func NewContext(ctx context.Context) (context.Context, *QueryTrace) {
	qt := &QueryTrace{}
	return context.WithValue(ctx, contextKey{}, qt), qt
}

// FromContext retrieves the QueryTrace stored by NewContext. Returns nil when
// no trace is present — all QueryTrace methods are nil-safe.
func FromContext(ctx context.Context) *QueryTrace {
	qt, _ := ctx.Value(contextKey{}).(*QueryTrace)
	return qt
}
