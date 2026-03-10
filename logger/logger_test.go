package logger_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/equinox/logger"
)

// newTestLogger creates a Logger that writes to the provided buffer.
func newTestLogger(buf *bytes.Buffer) *logger.Logger {
	return logger.New(buf)
}

func TestInfoWritesCorrectFormat(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	log.Info("connector", "kalshi", "fetching markets")

	got := buf.String()
	if !strings.Contains(got, "[INFO]") {
		t.Errorf("Info() output %q missing [INFO]", got)
	}
	if !strings.Contains(got, "[connector]") {
		t.Errorf("Info() output %q missing [connector]", got)
	}
	if !strings.Contains(got, "[kalshi]") {
		t.Errorf("Info() output %q missing [kalshi]", got)
	}
	if !strings.Contains(got, "fetching markets") {
		t.Errorf("Info() output %q missing message", got)
	}
}

func TestWarnWritesCorrectFormat(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	log.Warn("routing", "kalshi", "price data is stale")

	got := buf.String()
	if !strings.Contains(got, "[WARN]") {
		t.Errorf("Warn() output %q missing [WARN]", got)
	}
	if !strings.Contains(got, "[routing]") {
		t.Errorf("Warn() output %q missing [routing]", got)
	}
	if !strings.Contains(got, "[kalshi]") {
		t.Errorf("Warn() output %q missing [kalshi]", got)
	}
	if !strings.Contains(got, "price data is stale") {
		t.Errorf("Warn() output %q missing message", got)
	}
}

func TestErrorWritesCorrectFormatWithErr(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	underlying := fmt.Errorf("connection refused")
	log.Error("connector", "polymarket", "HTTP request failed", underlying)

	got := buf.String()
	if !strings.Contains(got, "[ERROR]") {
		t.Errorf("Error() output %q missing [ERROR]", got)
	}
	if !strings.Contains(got, "[connector]") {
		t.Errorf("Error() output %q missing [connector]", got)
	}
	if !strings.Contains(got, "[polymarket]") {
		t.Errorf("Error() output %q missing [polymarket]", got)
	}
	if !strings.Contains(got, "HTTP request failed") {
		t.Errorf("Error() output %q missing message", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("Error() output %q missing underlying error", got)
	}
}

func TestErrorWritesCorrectFormatWithNilErr(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	log.Error("server", "", "handler panicked", nil)

	got := buf.String()
	if !strings.Contains(got, "[ERROR]") {
		t.Errorf("Error() output %q missing [ERROR]", got)
	}
	if !strings.Contains(got, "handler panicked") {
		t.Errorf("Error() output %q missing message", got)
	}
}

func TestEmptyVenueAppearsInOutput(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	log.Info("routing", "", "no venue context")

	got := buf.String()
	if !strings.Contains(got, "[]") {
		t.Errorf("Info() output %q missing empty venue bracket []", got)
	}
}

func TestLoggerTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		level      string
		layer      string
		venue      string
		message    string
		err        error
		wantLevel  string
		wantLayer  string
		wantVenue  string
		wantMsg    string
		wantErrStr string
	}{
		{
			name:      "info connector kalshi",
			level:     "info",
			layer:     "connector",
			venue:     "kalshi",
			message:   "markets loaded",
			wantLevel: "[INFO]",
			wantLayer: "[connector]",
			wantVenue: "[kalshi]",
			wantMsg:   "markets loaded",
		},
		{
			name:      "warn equivalence no venue",
			level:     "warn",
			layer:     "equivalence",
			venue:     "",
			message:   "low confidence match",
			wantLevel: "[WARN]",
			wantLayer: "[equivalence]",
			wantVenue: "[]",
			wantMsg:   "low confidence match",
		},
		{
			name:       "error ai anthropic with err",
			level:      "error",
			layer:      "ai",
			venue:      "anthropic",
			message:    "API unavailable",
			err:        fmt.Errorf("503 Service Unavailable"),
			wantLevel:  "[ERROR]",
			wantLayer:  "[ai]",
			wantVenue:  "[anthropic]",
			wantMsg:    "API unavailable",
			wantErrStr: "503 Service Unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := newTestLogger(&buf)

			switch tt.level {
			case "info":
				log.Info(tt.layer, tt.venue, tt.message)
			case "warn":
				log.Warn(tt.layer, tt.venue, tt.message)
			case "error":
				log.Error(tt.layer, tt.venue, tt.message, tt.err)
			}

			got := buf.String()
			for _, want := range []string{tt.wantLevel, tt.wantLayer, tt.wantVenue, tt.wantMsg} {
				if want != "" && !strings.Contains(got, want) {
					t.Errorf("output %q missing %q", got, want)
				}
			}
			if tt.wantErrStr != "" && !strings.Contains(got, tt.wantErrStr) {
				t.Errorf("output %q missing error string %q", got, tt.wantErrStr)
			}
		})
	}
}

func TestEachCallProducesNewLine(t *testing.T) {
	var buf bytes.Buffer
	log := newTestLogger(&buf)

	log.Info("connector", "kalshi", "first")
	log.Info("connector", "kalshi", "second")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("expected at least 2 lines, got %d: %q", len(lines), buf.String())
	}
}
