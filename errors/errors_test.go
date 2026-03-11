package errors_test

import (
	"errors"
	"fmt"
	"testing"

	equinoxerrors "github.com/equinox/errors"
)

func TestEquinoxErrorFormatsWithUnderlyingError(t *testing.T) {
	underlying := fmt.Errorf("connection refused")
	err := &equinoxerrors.EquinoxError{
		Layer:   "connector",
		Venue:   "kalshi",
		Message: "failed to fetch markets",
		Err:     underlying,
	}

	got := err.Error()
	want := "[connector][kalshi] failed to fetch markets: connection refused"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestEquinoxErrorFormatsWithoutUnderlyingError(t *testing.T) {
	err := &equinoxerrors.EquinoxError{
		Layer:   "routing",
		Venue:   "",
		Message: "no venues available",
		Err:     nil,
	}

	got := err.Error()
	want := "[routing][] no venues available"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestEquinoxErrorUnwrapsUnderlying(t *testing.T) {
	underlying := fmt.Errorf("dial tcp timeout")
	err := &equinoxerrors.EquinoxError{
		Layer:   "connector",
		Venue:   "polymarket",
		Message: "HTTP request failed",
		Err:     underlying,
	}

	if !errors.Is(err, underlying) {
		t.Errorf("errors.Is(equinoxError, underlying) = false, want true")
	}
}

func TestEquinoxErrorUnwrapReturnsNilWhenNoUnderlying(t *testing.T) {
	err := &equinoxerrors.EquinoxError{
		Layer:   "server",
		Venue:   "",
		Message: "bad request",
		Err:     nil,
	}

	if err.Unwrap() != nil {
		t.Errorf("Unwrap() = %v, want nil", err.Unwrap())
	}
}

func TestEquinoxErrorTableDriven(t *testing.T) {
	sentinel := fmt.Errorf("sentinel")
	tests := []struct {
		name        string
		layer       string
		venue       string
		message     string
		err         error
		wantMessage string
	}{
		{
			name:        "connector kalshi with error",
			layer:       "connector",
			venue:       "kalshi",
			message:     "fetch failed",
			err:         sentinel,
			wantMessage: "[connector][kalshi] fetch failed: sentinel",
		},
		{
			name:        "ai anthropic with error",
			layer:       "ai",
			venue:       "anthropic",
			message:     "API call failed",
			err:         sentinel,
			wantMessage: "[ai][anthropic] API call failed: sentinel",
		},
		{
			name:        "routing no venue no error",
			layer:       "routing",
			venue:       "",
			message:     "score is NaN",
			err:         nil,
			wantMessage: "[routing][] score is NaN",
		},
		{
			name:        "config no venue no error",
			layer:       "config",
			venue:       "",
			message:     "OPENAI_API_KEY not set",
			err:         nil,
			wantMessage: "[config][] OPENAI_API_KEY not set",
		},
		{
			name:        "normalizer kalshi with error",
			layer:       "normalizer",
			venue:       "kalshi",
			message:     "price out of range",
			err:         sentinel,
			wantMessage: "[normalizer][kalshi] price out of range: sentinel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &equinoxerrors.EquinoxError{
				Layer:   tt.layer,
				Venue:   tt.venue,
				Message: tt.message,
				Err:     tt.err,
			}
			if got := e.Error(); got != tt.wantMessage {
				t.Errorf("Error() = %q, want %q", got, tt.wantMessage)
			}
		})
	}
}

func TestEquinoxErrorImplementsErrorInterface(t *testing.T) {
	var err error = &equinoxerrors.EquinoxError{
		Layer:   "server",
		Venue:   "",
		Message: "handler failed",
	}
	if err == nil {
		t.Error("EquinoxError does not satisfy error interface")
	}
}
