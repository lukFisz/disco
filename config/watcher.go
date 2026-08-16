package config

import (
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// watchInterval is the default interval at which Watcher polls the config
// file for changes.
const watchInterval = 2500 * time.Millisecond

// Watcher polls a config file on disk for changes and, on a successful
// reload, atomically swaps the config served by Current(). It never
// panics and never crashes the app on a bad reload — it just logs the
// error and keeps serving the last-known-good config.
type Watcher struct {
	path     string
	logger   *slog.Logger
	interval time.Duration

	current atomic.Pointer[Config]
	lastMod time.Time

	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewWatcher creates a Watcher for the config file at path. initial is the
// already-loaded Config to serve until the first successful reload.
// logger is used for the watcher's own operational logging (info on
// successful reload, warn/error on reload failure) — never nil in practice.
func NewWatcher(path string, initial *Config, logger *slog.Logger) *Watcher {
	return newWatcherWithInterval(path, initial, logger, watchInterval)
}

// newWatcherWithInterval is the unexported constructor NewWatcher delegates
// to, allowing tests to use a much shorter poll interval than the real
// watchInterval so they don't take multiple real seconds per test case.
func newWatcherWithInterval(path string, initial *Config, logger *slog.Logger, interval time.Duration) *Watcher {
	w := &Watcher{
		path:     path,
		logger:   logger,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
	w.current.Store(initial)

	if stat, err := os.Stat(path); err == nil {
		w.lastMod = stat.ModTime()
	}

	return w
}

// Current returns the most recently successfully loaded Config. Safe for
// concurrent use from any goroutine.
func (w *Watcher) Current() *Config {
	return w.current.Load()
}

// Start begins polling the config file every interval in a background
// goroutine and returns immediately. On each tick, it stats the file; if
// the mtime changed since the last check, it reloads+validates via
// Load(path). On success, it atomically swaps the current config and — if
// onReload is non-nil — calls onReload(newConfig). On failure (parse or
// validation error), it logs the error via logger.Error and keeps serving
// the previous config (never panics, never crashes the app).
func (w *Watcher) Start(onReload func(*Config)) {
	go w.loop(onReload)
}

func (w *Watcher) loop(onReload func(*Config)) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.checkAndReload(onReload)
		}
	}
}

func (w *Watcher) checkAndReload(onReload func(*Config)) {
	stat, err := os.Stat(w.path)
	if err != nil {
		w.logger.Warn("config file stat failed, keeping previous config", "path", w.path, "error", err)
		return
	}

	modTime := stat.ModTime()
	if modTime.Equal(w.lastMod) {
		return
	}

	// Update lastMod before attempting to load, so a persistently broken
	// file doesn't get retried every single tick.
	w.lastMod = modTime

	newCfg, err := Load(w.path)
	if err != nil {
		w.logger.Error("config reload failed, keeping previous config", "path", w.path, "error", err)
		return
	}

	w.current.Store(newCfg)
	w.logger.Info("config reloaded", "path", w.path, "commands", len(newCfg.Commands), "log_level", newCfg.LogLevel)

	if onReload != nil {
		onReload(newCfg)
	}
}

// Stop terminates the polling goroutine. Safe to call at most once (calling
// it more than once is also safe — the underlying channel close is
// guarded by sync.Once — but callers should not rely on that).
func (w *Watcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}
