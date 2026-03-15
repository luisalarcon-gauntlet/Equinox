// Package trace provides a lightweight, context-injected query path recorder.
// Each layer in the request pipeline calls trace.FromContext(ctx).Add("...") to
// append a human-readable step describing what path it took. The server reads
// the final slice and includes it in the SearchResponse as query_path.
//
// The pattern mirrors equivalence.AIStatsCollector: a zero-import package,
// injected via a context key, nil-safe so layers work fine in tests that do
// not inject a trace.
package trace

import (
	"context"
	"sync"
)

type traceContextKey struct{}

// QueryTrace records the ordered steps a query took through the system.
// All methods are nil-safe and goroutine-safe.
type QueryTrace struct {
	mu    sync.Mutex
	steps []string
}

// Add appends a step to the trace. No-op if the receiver is nil.
func (t *QueryTrace) Add(step string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.steps = append(t.steps, step)
	t.mu.Unlock()
}

// Steps returns a snapshot of the recorded steps in insertion order.
// Returns nil if the receiver is nil or no steps have been recorded.
func (t *QueryTrace) Steps() []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.steps) == 0 {
		return nil
	}
	out := make([]string, len(t.steps))
	copy(out, t.steps)
	return out
}

// NewContext creates a fresh QueryTrace, attaches it to ctx, and returns both.
func NewContext(ctx context.Context) (context.Context, *QueryTrace) {
	qt := &QueryTrace{}
	return context.WithValue(ctx, traceContextKey{}, qt), qt
}

// FromContext retrieves the QueryTrace from ctx.
// Returns nil if no trace was injected — all QueryTrace methods are nil-safe.
func FromContext(ctx context.Context) *QueryTrace {
	qt, _ := ctx.Value(traceContextKey{}).(*QueryTrace)
	return qt
}
