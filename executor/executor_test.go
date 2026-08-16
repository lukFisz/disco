package executor

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"disco/config"
)

func boolPtr(b bool) *bool {
	return &b
}

func TestSubstitute(t *testing.T) {
	tests := []struct {
		name   string
		exec   []string
		params map[string]string
		want   []string
	}{
		{
			name:   "whole arg substitution",
			exec:   []string{"/bin/echo", "{msg}"},
			params: map[string]string{"msg": "hello"},
			want:   []string{"/bin/echo", "hello"},
		},
		{
			name:   "embedded substitution",
			exec:   []string{"/usr/bin/systemctl", "restart", "--service={service}"},
			params: map[string]string{"service": "nginx"},
			want:   []string{"/usr/bin/systemctl", "restart", "--service=nginx"},
		},
		{
			name:   "multiple placeholders in one arg",
			exec:   []string{"/bin/echo", "{a}-{b}-{a}"},
			params: map[string]string{"a": "x", "b": "y"},
			want:   []string{"/bin/echo", "x-y-x"},
		},
		{
			name:   "unused param is not an error, simply unused",
			exec:   []string{"/bin/echo", "static"},
			params: map[string]string{"unused": "value"},
			want:   []string{"/bin/echo", "static"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := substitute(tt.exec, tt.params)
			if len(got) != len(tt.want) {
				t.Fatalf("substitute() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("substitute()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestRun_Echo(t *testing.T) {
	e := New()
	cmd := config.Command{
		Name:            "echotest",
		Exec:            []string{"/bin/echo", "{msg}"},
		Timeout:         config.Duration(5 * time.Second),
		AllowConcurrent: boolPtr(true),
	}

	res, err := e.Run(context.Background(), cmd, map[string]string{"msg": "hello world"}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.Err != nil {
		t.Fatalf("res.Err = %v, want nil", res.Err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if strings.TrimSpace(res.Stdout) != "hello world" {
		t.Errorf("Stdout = %q, want %q", res.Stdout, "hello world")
	}
	if res.TimedOut {
		t.Errorf("TimedOut = true, want false")
	}
}

func TestRun_Timeout(t *testing.T) {
	e := New()
	cmd := config.Command{
		Name:            "sleeptest",
		Exec:            []string{"sleep", "5"},
		Timeout:         config.Duration(100 * time.Millisecond),
		AllowConcurrent: boolPtr(true),
	}

	res, err := e.Run(context.Background(), cmd, nil, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !res.TimedOut {
		t.Errorf("TimedOut = false, want true")
	}
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1", res.ExitCode)
	}
	if res.Duration >= 1*time.Second {
		t.Errorf("Duration = %v, want < 1s (should be close to 100ms timeout, not 5s sleep)", res.Duration)
	}
}

func TestRun_AllowConcurrentFalse(t *testing.T) {
	e := New()
	cmd := config.Command{
		Name:            "nonconcurrent",
		Exec:            []string{"/bin/sh", "-c", "sleep 0.2"},
		Timeout:         config.Duration(5 * time.Second),
		AllowConcurrent: boolPtr(false),
	}

	var wg sync.WaitGroup
	results := make([]error, 2)
	durations := make([]time.Duration, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		callStart := time.Now()
		_, err := e.Run(context.Background(), cmd, nil, nil)
		durations[0] = time.Since(callStart)
		results[0] = err
	}()

	// Give the first goroutine a head start to acquire the lock.
	time.Sleep(30 * time.Millisecond)

	go func() {
		defer wg.Done()
		callStart := time.Now()
		_, err := e.Run(context.Background(), cmd, nil, nil)
		durations[1] = time.Since(callStart)
		results[1] = err
	}()

	wg.Wait()

	// Exactly one of the two should have been rejected.
	rejectedCount := 0
	var rejectedDuration time.Duration
	for i, err := range results {
		if err == ErrAlreadyRunning {
			rejectedCount++
			rejectedDuration = durations[i]
		}
	}
	if rejectedCount != 1 {
		t.Fatalf("expected exactly 1 rejection, got %d (results: %v)", rejectedCount, results)
	}

	// The rejected call should have returned promptly (it never blocks
	// waiting for the other invocation), not after ~200ms.
	if rejectedDuration >= 150*time.Millisecond {
		t.Errorf("rejected call took %v, want well under the 200ms sleep duration", rejectedDuration)
	}
}

func TestRun_AllowConcurrentTrue(t *testing.T) {
	e := New()
	cmd := config.Command{
		Name:            "concurrent",
		Exec:            []string{"/bin/sh", "-c", "sleep 0.2"},
		Timeout:         config.Duration(5 * time.Second),
		AllowConcurrent: boolPtr(true),
	}

	var wg sync.WaitGroup
	results := make([]error, 2)

	wg.Add(2)
	for i := 0; i < 2; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, err := e.Run(context.Background(), cmd, nil, nil)
			results[i] = err
		}()
	}
	wg.Wait()

	for i, err := range results {
		if err != nil {
			t.Errorf("results[%d] = %v, want nil", i, err)
		}
	}
}

func TestRun_NonexistentBinary(t *testing.T) {
	e := New()
	cmd := config.Command{
		Name:            "badbinary",
		Exec:            []string{"/nonexistent/binary-xyz"},
		Timeout:         config.Duration(5 * time.Second),
		AllowConcurrent: boolPtr(true),
	}

	res, err := e.Run(context.Background(), cmd, nil, nil)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil (error should be on Result.Err)", err)
	}
	if res.Err == nil {
		t.Fatalf("res.Err = nil, want non-nil for a failed-to-start binary")
	}
}

func TestRun_NonZeroExit_DistinctFromStartFailure(t *testing.T) {
	e := New()
	cmd := config.Command{
		Name:            "exit1",
		Exec:            []string{"/bin/sh", "-c", "exit 1"},
		Timeout:         config.Duration(5 * time.Second),
		AllowConcurrent: boolPtr(true),
	}

	res, err := e.Run(context.Background(), cmd, nil, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if res.Err != nil {
		t.Errorf("res.Err = %v, want nil for a normal non-zero exit", res.Err)
	}
	if res.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", res.ExitCode)
	}
}

func TestRun_StdoutStderrSeparate(t *testing.T) {
	e := New()
	cmd := config.Command{
		Name:            "outerr",
		Exec:            []string{"/bin/sh", "-c", "echo out; echo err >&2"},
		Timeout:         config.Duration(5 * time.Second),
		AllowConcurrent: boolPtr(true),
	}

	res, err := e.Run(context.Background(), cmd, nil, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "out" {
		t.Errorf("Stdout = %q, want %q", res.Stdout, "out")
	}
	if strings.TrimSpace(res.Stderr) != "err" {
		t.Errorf("Stderr = %q, want %q", res.Stderr, "err")
	}
}

func TestRun_StreamMode(t *testing.T) {
	e := New()
	cmd := config.Command{
		Name:            "streamtest",
		Exec:            []string{"/bin/sh", "-c", "echo a; sleep 0.3; echo b; sleep 0.3; echo c"},
		Timeout:         config.Duration(5 * time.Second),
		AllowConcurrent: boolPtr(true),
	}

	var mu sync.Mutex
	var snapshots []string

	onProgress := func(stdout, stderr string) {
		mu.Lock()
		defer mu.Unlock()
		snapshots = append(snapshots, stdout)
	}

	res, err := e.Run(context.Background(), cmd, nil, onProgress)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(snapshots) < 1 {
		t.Fatalf("expected at least 1 progress callback (the final one), got 0")
	}

	final := snapshots[len(snapshots)-1]
	if strings.TrimSpace(final) != strings.TrimSpace(res.Stdout) {
		t.Errorf("final snapshot = %q, want it to match final Result.Stdout = %q", final, res.Stdout)
	}
	if strings.TrimSpace(res.Stdout) != "a\nb\nc" {
		t.Errorf("res.Stdout = %q, want %q", res.Stdout, "a\nb\nc")
	}

	// If there was more than one snapshot, content should have grown
	// (non-decreasing length) across calls.
	for i := 1; i < len(snapshots); i++ {
		if len(snapshots[i]) < len(snapshots[i-1]) {
			t.Errorf("snapshot %d shorter than snapshot %d: %q vs %q", i, i-1, snapshots[i], snapshots[i-1])
		}
	}
}
