// Package audit provides structured (JSON) logging of command executions
// for the disco bot, with log rotation. It knows nothing about Discord.
package audit

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"disco/config"

	"gopkg.in/natefinch/lumberjack.v2"
)

// New builds a structured (JSON) logger that writes to a rotating log
// file, using lumberjack for rotation (size/backups/age/compress) and
// stdlib log/slog for structured JSON records. If alsoStdout is true,
// records are written to both the rotating file and os.Stdout.
//
// The returned value is a bare *slog.Logger rather than a custom wrapper
// type: slog.NewJSONHandler already stamps every record with an automatic
// "time" field plus whatever structured attributes the caller supplies,
// which is all that's needed to satisfy "who/what/when" for an audit
// trail. A later "discord" package task is expected to call the returned
// logger directly at each command invocation, e.g.:
//
//	auditLogger.Info("command_invocation",
//		slog.String("event", "completed"), // or "rejected" / "error"
//		slog.String("reason", ""),          // populated for rejected/error events,
//		                                     // e.g. unknown_command|invalid_param|already_running|start_failed
//		slog.String("user_id", "..."),
//		slog.String("username", "..."),
//		slog.String("command", "..."),
//		slog.Any("params", map[string]string{...}),
//		slog.Int("exit_code", 0),
//		slog.Int64("duration_ms", 0),
//		slog.Bool("timed_out", false),
//	)
//
// No wrapper/abstraction is built around slog.Logger here on purpose
// (YAGNI): callers get the full stdlib slog API for free.
func New(cfg config.AuditConfig, alsoStdout bool) (*slog.Logger, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.File), 0o755); err != nil {
		return nil, fmt.Errorf("creating audit log directory: %w", err)
	}
	lj := &lumberjack.Logger{
		Filename:   cfg.File,
		MaxSize:    cfg.MaxSizeMB,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAgeDays,
		Compress:   cfg.Compress,
	}
	var w io.Writer = lj
	if alsoStdout {
		w = io.MultiWriter(lj, os.Stdout)
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})), nil
}
