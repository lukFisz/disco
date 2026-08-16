package audit

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"disco/config"
)

// readLastJSONLine reads the file at path, splits it into non-empty lines,
// and unmarshals the last one as a JSON object.
func readLastJSONLine(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("log file %s is empty", path)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	var last string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			last = l
		}
	}
	if last == "" {
		t.Fatalf("no non-empty lines found in log file %s", path)
	}
	var record map[string]interface{}
	if err := json.Unmarshal([]byte(last), &record); err != nil {
		t.Fatalf("unmarshaling log line %q: %v", last, err)
	}
	return record
}

func TestNew_CreatesNestedDirAndWritesJSON(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "nested", "audit.log")

	cfg := config.AuditConfig{
		File:       logFile,
		MaxSizeMB:  10,
		MaxBackups: 3,
		MaxAgeDays: 7,
		Compress:   false,
	}

	logger, err := New(cfg, false)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if logger == nil {
		t.Fatal("New returned nil logger")
	}

	logger.Info("command_invocation",
		slog.String("event", "completed"),
		slog.String("command", "deploy"),
		slog.Int("exit_code", 0),
	)

	record := readLastJSONLine(t, logFile)

	for _, key := range []string{"time", "level", "msg", "event", "command", "exit_code"} {
		if _, ok := record[key]; !ok {
			t.Errorf("expected key %q in log record, got %v", key, record)
		}
	}

	if got, want := record["msg"], "command_invocation"; got != want {
		t.Errorf("msg = %v, want %v", got, want)
	}
	if got, want := record["event"], "completed"; got != want {
		t.Errorf("event = %v, want %v", got, want)
	}
	if got, want := record["command"], "deploy"; got != want {
		t.Errorf("command = %v, want %v", got, want)
	}
	if got, want := record["exit_code"], float64(0); got != want {
		t.Errorf("exit_code = %v, want %v", got, want)
	}
	if got, want := record["level"], "INFO"; got != want {
		t.Errorf("level = %v, want %v", got, want)
	}
}

func TestNew_AlsoStdoutDoesNotError(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "nested", "audit.log")

	cfg := config.AuditConfig{
		File:       logFile,
		MaxSizeMB:  10,
		MaxBackups: 3,
		MaxAgeDays: 7,
		Compress:   false,
	}

	// Temporarily redirect os.Stdout to a pipe so the MultiWriter path is
	// exercised without spamming the test output, then restore it.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	logger, err := New(cfg, true)
	if err != nil {
		w.Close()
		r.Close()
		t.Fatalf("New returned error: %v", err)
	}
	if logger == nil {
		w.Close()
		r.Close()
		t.Fatal("New returned nil logger")
	}

	logger.Info("command_invocation",
		slog.String("event", "completed"),
		slog.String("command", "deploy"),
		slog.Int("exit_code", 0),
	)

	w.Close()
	r.Close()

	// The file should still get written correctly regardless of stdout.
	record := readLastJSONLine(t, logFile)
	if got, want := record["msg"], "command_invocation"; got != want {
		t.Errorf("msg = %v, want %v", got, want)
	}
}
