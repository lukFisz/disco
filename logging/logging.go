// Package logging builds the application-wide *slog.Logger used for
// disco's own operational logging (as distinct from the audit package's
// structured JSON audit trail of command executions).
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// ParseLevel converts a config log level string ("debug", "info", "warn",
// "error", case-insensitive; "" treated as "info") to a slog.Level.
func ParseLevel(level string) (slog.Level, error) {
	switch strings.ToLower(level) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q", level)
	}
}

// New builds a *slog.Logger that writes human-readable text records to
// stdout, backed by a *slog.LevelVar so the level can be adjusted at
// runtime (e.g. after a config reload) without rebuilding the logger.
func New(level string) (*slog.Logger, *slog.LevelVar, error) {
	return newWithWriter(level, os.Stdout)
}

// newWithWriter is the unexported constructor New delegates to, allowing
// tests to point the handler at an in-memory buffer instead of stdout.
func newWithWriter(level string, w io.Writer) (*slog.Logger, *slog.LevelVar, error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return nil, nil, err
	}

	levelVar := new(slog.LevelVar)
	levelVar.Set(lvl)

	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: levelVar}))
	return logger, levelVar, nil
}
