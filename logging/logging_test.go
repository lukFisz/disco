package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    slog.Level
		wantErr bool
	}{
		{name: "debug", input: "debug", want: slog.LevelDebug},
		{name: "info", input: "info", want: slog.LevelInfo},
		{name: "warn", input: "warn", want: slog.LevelWarn},
		{name: "error", input: "error", want: slog.LevelError},
		{name: "empty treated as info", input: "", want: slog.LevelInfo},
		{name: "uppercase", input: "DEBUG", want: slog.LevelDebug},
		{name: "mixed case", input: "WaRn", want: slog.LevelWarn},
		{name: "invalid", input: "verbose", wantErr: true},
		{name: "invalid trace", input: "trace", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLevel(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseLevel(%q) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLevel(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewWithWriter_InvalidLevel(t *testing.T) {
	var buf bytes.Buffer
	logger, levelVar, err := newWithWriter("nonsense", &buf)
	if err == nil {
		t.Fatal("expected error for invalid level, got nil")
	}
	if logger != nil {
		t.Errorf("expected nil logger on error, got %v", logger)
	}
	if levelVar != nil {
		t.Errorf("expected nil levelvar on error, got %v", levelVar)
	}
}

func TestNewWithWriter_LogsAtConfiguredLevel(t *testing.T) {
	var buf bytes.Buffer
	logger, levelVar, err := newWithWriter("info", &buf)
	if err != nil {
		t.Fatalf("newWithWriter returned error: %v", err)
	}
	if logger == nil || levelVar == nil {
		t.Fatalf("expected non-nil logger and levelvar")
	}

	logger.Info("hello world")
	if !strings.Contains(buf.String(), "hello world") {
		t.Errorf("expected output to contain log message, got %q", buf.String())
	}
}

func TestNewWithWriter_LevelVarMutationTakesEffect(t *testing.T) {
	var buf bytes.Buffer
	logger, levelVar, err := newWithWriter("info", &buf)
	if err != nil {
		t.Fatalf("newWithWriter returned error: %v", err)
	}

	// Raise the level to Error at runtime; Info logs should now be
	// suppressed without rebuilding the logger.
	levelVar.Set(slog.LevelError)

	buf.Reset()
	logger.Info("should be suppressed")
	if buf.Len() != 0 {
		t.Errorf("expected no output after raising level to Error, got %q", buf.String())
	}

	logger.Error("should appear")
	if !strings.Contains(buf.String(), "should appear") {
		t.Errorf("expected error-level output to appear, got %q", buf.String())
	}
}

func TestNew_BuildsWorkingLogger(t *testing.T) {
	logger, levelVar, err := New("debug")
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
	if levelVar == nil {
		t.Fatal("expected non-nil levelvar")
	}
	if levelVar.Level() != slog.LevelDebug {
		t.Errorf("levelVar.Level() = %v, want %v", levelVar.Level(), slog.LevelDebug)
	}
}
