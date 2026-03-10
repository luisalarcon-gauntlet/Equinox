package errors

import "fmt"

// EquinoxError is the single error type used throughout the system.
// Every error must be wrapped in this struct — never return raw errors.
type EquinoxError struct {
	Layer   string // "connector", "normalizer", "equivalence", "ai", "routing", "server", "config"
	Venue   string // "kalshi", "polymarket", "anthropic", "" (empty if not venue-specific)
	Message string // human-readable description of what failed
	Err     error  // underlying error (may be nil)
}

// Error implements the error interface.
// Format: [layer][venue] message  or  [layer][venue] message: underlying
func (e *EquinoxError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s][%s] %s: %v", e.Layer, e.Venue, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s][%s] %s", e.Layer, e.Venue, e.Message)
}

// Unwrap returns the underlying error so errors.Is and errors.As work correctly.
func (e *EquinoxError) Unwrap() error {
	return e.Err
}
