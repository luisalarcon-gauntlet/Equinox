package logger

import (
	"fmt"
	"io"
	"os"
	"time"
)

// Logger writes structured log lines to an io.Writer.
// Format: [LEVEL][layer][venue] message (: err if present)
type Logger struct {
	out io.Writer
}

// New creates a Logger that writes to w.
func New(out io.Writer) *Logger {
	return &Logger{out: out}
}

// Default returns a Logger that writes to stdout.
func Default() *Logger {
	return New(os.Stdout)
}

// Debug logs verbose diagnostic information, typically raw API data.
func (l *Logger) Debug(layer, venue, message string) {
	l.write("DEBUG", layer, venue, message, nil)
}

// Info logs a normal operational event.
func (l *Logger) Info(layer, venue, message string) {
	l.write("INFO", layer, venue, message, nil)
}

// Warn logs a degraded-but-continuing condition.
func (l *Logger) Warn(layer, venue, message string) {
	l.write("WARN", layer, venue, message, nil)
}

// Error logs a failed operation along with its underlying error.
func (l *Logger) Error(layer, venue, message string, err error) {
	l.write("ERROR", layer, venue, message, err)
}

func (l *Logger) write(level, layer, venue, message string, err error) {
	ts := time.Now().UTC().Format(time.RFC3339)
	var line string
	if err != nil {
		line = fmt.Sprintf("%s [%s][%s][%s] %s: %v\n", ts, level, layer, venue, message, err)
	} else {
		line = fmt.Sprintf("%s [%s][%s][%s] %s\n", ts, level, layer, venue, message)
	}
	fmt.Fprint(l.out, line)
}
