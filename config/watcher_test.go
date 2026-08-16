package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testWatchInterval = 20 * time.Millisecond

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const watcherValidYAML = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  cmd:
    exec: ["./run.sh"]
`

const watcherValidYAMLDebug = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
log_level: debug
commands:
  cmd:
    exec: ["./run.sh"]
  other:
    exec: ["./other.sh"]
`

const watcherInvalidYAML = `
defaults:
  timeout: 10s
audit:
  file: /tmp/audit.log
commands:
  cmd:
    exec: []
`

// writeAtDistinctMTime rewrites path with contents and ensures the file's
// mtime differs from before, forcing it via os.Chtimes if necessary to
// avoid flakiness from coarse filesystem mtime granularity.
func writeAtDistinctMTime(t *testing.T, path string, contents string) {
	t.Helper()

	var before time.Time
	if stat, err := os.Stat(path); err == nil {
		before = stat.ModTime()
	}

	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	stat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat after write: %v", err)
	}

	if !stat.ModTime().After(before) {
		newTime := before.Add(time.Second)
		if err := os.Chtimes(path, newTime, newTime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
}

func TestWatcher_CurrentReturnsInitialBeforeChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(watcherValidYAML), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	initial, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	w := newWatcherWithInterval(path, initial, testLogger(), testWatchInterval)
	if w.Current() != initial {
		t.Errorf("Current() = %v, want initial config %v", w.Current(), initial)
	}
}

func TestWatcher_ReloadsOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(watcherValidYAML), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	initial, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	w := newWatcherWithInterval(path, initial, testLogger(), testWatchInterval)

	reloaded := make(chan *Config, 1)
	w.Start(func(cfg *Config) {
		reloaded <- cfg
	})
	defer w.Stop()

	writeAtDistinctMTime(t, path, watcherValidYAMLDebug)

	select {
	case cfg := <-reloaded:
		if cfg.LogLevel != "debug" {
			t.Errorf("reloaded config LogLevel = %q, want %q", cfg.LogLevel, "debug")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onReload to be called")
	}

	if got := w.Current(); got.LogLevel != "debug" {
		t.Errorf("Current().LogLevel = %q, want %q", got.LogLevel, "debug")
	}
	if len(w.Current().Commands) != 2 {
		t.Errorf("Current() has %d commands, want 2", len(w.Current().Commands))
	}
}

func TestWatcher_InvalidReloadKeepsPreviousConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(watcherValidYAML), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	initial, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	w := newWatcherWithInterval(path, initial, testLogger(), testWatchInterval)

	var reloadCalled bool
	w.Start(func(cfg *Config) {
		reloadCalled = true
	})
	defer w.Stop()

	writeAtDistinctMTime(t, path, watcherInvalidYAML)

	// Give the watcher several ticks to notice and (fail to) reload.
	time.Sleep(10 * testWatchInterval)

	if reloadCalled {
		t.Error("onReload should not have been called for an invalid config")
	}
	if w.Current() != initial {
		t.Errorf("Current() = %v, want unchanged initial config %v", w.Current(), initial)
	}
}

func TestWatcher_StopHaltsPolling(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(watcherValidYAML), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	initial, err := Load(path)
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	w := newWatcherWithInterval(path, initial, testLogger(), testWatchInterval)
	w.Start(nil)
	w.Stop()

	// Give the goroutine time to actually exit before mutating the file.
	time.Sleep(5 * testWatchInterval)

	writeAtDistinctMTime(t, path, watcherValidYAMLDebug)

	// Wait past several would-be ticks; Current() should never update
	// since polling has stopped.
	time.Sleep(10 * testWatchInterval)

	if w.Current() != initial {
		t.Errorf("Current() changed after Stop(), want it to remain the initial config")
	}

	// Calling Stop again must not panic.
	w.Stop()
}
