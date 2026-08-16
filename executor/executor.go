// Package executor runs the shell commands defined in the disco config and
// captures/formats their output. It knows nothing about Discord: it deals
// only in plain Go types (config.Command in, Result/Formatted out) so that a
// later "discord" package can consume it without this package importing
// discordgo.
package executor

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"disco/config"
)

// maxBufferSize caps how much output (per stream) we buffer in memory for a
// single command execution. Once hit, further writes are silently dropped.
const maxBufferSize = 1 << 20 // 1MB

// Result is the outcome of running a single command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int // -1 for timeout-kill or start failure
	TimedOut bool
	Err      error // non-nil ONLY for "failed to start" - NOT set for a normal non-zero exit
	Duration time.Duration
	// Truncated is true if either stdout or stderr hit the 1MB capture cap.
	Truncated bool
}

// ErrAlreadyRunning is returned by Run when the command does not allow
// concurrent invocations and another invocation of the same command name is
// already in flight.
var ErrAlreadyRunning = errors.New("command already running")

// StreamFunc is invoked periodically (throttled) and once finally while a
// command runs, only when the caller wants intermediate progress (stream
// output mode). It receives the accumulated stdout/stderr so far.
type StreamFunc func(stdout, stderr string)

// Executor runs commands, enforcing the per-command-name non-concurrency
// rule declared by config.Command.AllowConcurrent.
type Executor struct {
	mu     sync.Mutex
	locks  map[string]*sync.Mutex // keyed by command name
	logger *slog.Logger
}

// New returns a ready-to-use Executor.
func New() *Executor {
	return &Executor{
		locks:  make(map[string]*sync.Mutex),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// SetLogger installs l as the executor's operational logger for
// subsequent Run calls. A nil l is ignored (the discard logger from New
// remains in effect).
func (e *Executor) SetLogger(l *slog.Logger) {
	if l != nil {
		e.logger = l
	}
}

// lockFor returns the (get-or-create) mutex for the given command name.
func (e *Executor) lockFor(name string) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.locks[name]
	if !ok {
		l = &sync.Mutex{}
		e.locks[name] = l
	}
	return l
}

// Run executes cmd.Exec with params substituted into argv. If
// *cmd.AllowConcurrent is false, it uses a non-blocking per-command-name
// lock (sync.Mutex.TryLock) and returns (nil, ErrAlreadyRunning)
// IMMEDIATELY if another invocation of the same command name is in
// flight - it never blocks waiting for the other invocation to finish. If
// *cmd.AllowConcurrent is true, no locking happens at all.
//
// onProgress may be nil (only used in stream mode); when non-nil it's
// called roughly once per second with a snapshot of accumulated
// stdout/stderr while the process runs, and once more with the final
// content right before Run returns.
func (e *Executor) Run(ctx context.Context, cmd config.Command, params map[string]string, onProgress StreamFunc) (*Result, error) {
	if cmd.AllowConcurrent == nil || !*cmd.AllowConcurrent {
		lock := e.lockFor(cmd.Name)
		if !lock.TryLock() {
			e.logger.Warn("command already running, rejecting", "command", cmd.Name)
			return nil, ErrAlreadyRunning
		}
		defer lock.Unlock()
	}

	e.logger.Info("executing command", "command", cmd.Name, "timeout", time.Duration(cmd.Timeout).String())

	argv := substitute(cmd.Exec, params)
	program := argv[0]
	args := argv[1:]

	e.logger.Info("command started", "command", cmd.Name, "program", program, "args", args)

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(cmd.Timeout))
	defer cancel()

	execCmd := exec.CommandContext(runCtx, program, args...)

	var stdoutBuf, stderrBuf syncBuffer
	execCmd.Stdout = &stdoutBuf
	execCmd.Stderr = &stderrBuf

	start := time.Now()

	if err := execCmd.Start(); err != nil {
		e.logger.Error("failed to start command", "command", cmd.Name, "error", err)
		return &Result{Err: err}, nil
	}

	var wg sync.WaitGroup
	var done chan struct{}
	if onProgress != nil {
		done = make(chan struct{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()
			var lastStdout, lastStderr string
			for {
				select {
				case <-ticker.C:
					out, errOut := stdoutBuf.String(), stderrBuf.String()
					if out != lastStdout || errOut != lastStderr {
						lastStdout, lastStderr = out, errOut
						onProgress(out, errOut)
					}
				case <-done:
					return
				}
			}
		}()
	}

	waitErr := execCmd.Wait()

	if done != nil {
		close(done)
		wg.Wait()
	}

	duration := time.Since(start)

	res := &Result{
		Stdout:    stdoutBuf.String(),
		Stderr:    stderrBuf.String(),
		Duration:  duration,
		Truncated: stdoutBuf.truncated || stderrBuf.truncated,
	}

	switch {
	case waitErr == nil:
		res.ExitCode = 0
	default:
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		}
	}

	if runCtx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		res.ExitCode = -1
	}

	if onProgress != nil {
		onProgress(res.Stdout, res.Stderr)
	}

	e.logger.Info("command finished", "command", cmd.Name, "exit_code", res.ExitCode, "duration_ms", duration.Milliseconds(), "timed_out", res.TimedOut, "truncated", res.Truncated)

	return res, nil
}

// substitute applies {paramname} -> value replacement to the program name
// and every argument in exec, using a plain string replacer. This runs
// BEFORE building the exec.Cmd, so substituted values are always inert
// literal argv bytes - never interpreted by a shell.
func substitute(execArgv []string, params map[string]string) []string {
	pairs := make([]string, 0, len(params)*2)
	for k, v := range params {
		pairs = append(pairs, "{"+k+"}", v)
	}
	replacer := strings.NewReplacer(pairs...)

	out := make([]string, len(execArgv))
	for i, s := range execArgv {
		out[i] = replacer.Replace(s)
	}
	return out
}

// syncBuffer is a mutex-guarded, size-capped byte buffer used to capture a
// running command's stdout/stderr. Writes beyond maxBufferSize are silently
// dropped (never an error) and set truncated to true.
type syncBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	truncated bool
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	remaining := maxBufferSize - s.buf.Len()
	if remaining <= 0 {
		s.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		s.buf.Write(p[:remaining])
		s.truncated = true
		return len(p), nil
	}
	s.buf.Write(p)
	return len(p), nil
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
