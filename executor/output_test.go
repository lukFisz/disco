package executor

import (
	"strings"
	"testing"
	"time"

	"disco/config"
)

func TestFormat_ShortOutput(t *testing.T) {
	res := &Result{
		Stdout:   "hello\n",
		ExitCode: 0,
	}
	got := Format("mycmd", config.OutputFull, res)

	if got.Attachment != nil {
		t.Errorf("Attachment = %v, want nil for short output", got.Attachment)
	}
	if len(got.Content) > maxDiscordContentLen {
		t.Errorf("Content length = %d, want <= %d", len(got.Content), maxDiscordContentLen)
	}
	if !strings.Contains(got.Content, "Exit code: 0") {
		t.Errorf("Content = %q, want it to contain %q", got.Content, "Exit code: 0")
	}
	if !strings.Contains(got.Content, "hello") {
		t.Errorf("Content = %q, want it to contain %q", got.Content, "hello")
	}
}

func TestFormat_LongOutputTruncatesAndAttaches(t *testing.T) {
	longOutput := strings.Repeat("x", maxDiscordContentLen*3)
	res := &Result{
		Stdout:   longOutput,
		ExitCode: 0,
	}
	got := Format("mycmd", config.OutputFull, res)

	if len(got.Content) > maxDiscordContentLen {
		t.Errorf("Content length = %d, want <= %d", len(got.Content), maxDiscordContentLen)
	}
	if !strings.Contains(got.Content, "truncated") {
		t.Errorf("Content = %q, want it to mention truncation", got.Content)
	}
	if got.Attachment == nil {
		t.Fatalf("Attachment = nil, want non-nil for long output")
	}
	if got.Attachment.Filename != "mycmd-output.txt" {
		t.Errorf("Attachment.Filename = %q, want %q", got.Attachment.Filename, "mycmd-output.txt")
	}
	if !strings.Contains(string(got.Attachment.Data), longOutput) {
		t.Errorf("Attachment.Data does not contain the full untruncated output")
	}
}

func TestFormat_SilentModeNeverIncludesOutput(t *testing.T) {
	res := &Result{
		Stdout:   "secret stdout content",
		Stderr:   "secret stderr content",
		ExitCode: 3,
	}
	got := Format("mycmd", config.OutputSilent, res)

	if got.Content != "Exit code: 3" {
		t.Errorf("Content = %q, want %q", got.Content, "Exit code: 3")
	}
	if strings.Contains(got.Content, "secret") {
		t.Errorf("Content = %q, must never contain stdout/stderr in silent mode", got.Content)
	}
	if got.Attachment != nil {
		t.Errorf("Attachment = %v, want nil in silent mode", got.Attachment)
	}
}

func TestFormat_TimedOutMentionsTimeout(t *testing.T) {
	res := &Result{
		ExitCode: -1,
		TimedOut: true,
		Duration: 5 * time.Second,
	}
	got := Format("mycmd", config.OutputFull, res)

	if !strings.Contains(strings.ToLower(got.Content), "timed out") {
		t.Errorf("Content = %q, want it to mention timeout", got.Content)
	}
}

func TestFormat_SilentModeMentionsTimeout(t *testing.T) {
	res := &Result{
		ExitCode: -1,
		TimedOut: true,
		Duration: 5 * time.Second,
	}
	got := Format("mycmd", config.OutputSilent, res)

	if !strings.Contains(strings.ToLower(got.Content), "timed out") {
		t.Errorf("Content = %q, want it to mention timeout even in silent mode", got.Content)
	}
	if strings.Contains(got.Content, "secret") {
		t.Errorf("sanity check failed")
	}
}

func TestFormatProgress_ShortContent(t *testing.T) {
	got := FormatProgress("some output\n", "")

	if len(got) > maxDiscordContentLen {
		t.Errorf("len(got) = %d, want <= %d", len(got), maxDiscordContentLen)
	}
	if !strings.Contains(got, "running") {
		t.Errorf("got = %q, want it to contain the running marker", got)
	}
	if !strings.Contains(got, "some output") {
		t.Errorf("got = %q, want it to contain the output", got)
	}
}

func TestFormatProgress_LongContentTruncates(t *testing.T) {
	longOutput := strings.Repeat("y", maxDiscordContentLen*3)
	got := FormatProgress(longOutput, "")

	if len(got) > maxDiscordContentLen {
		t.Errorf("len(got) = %d, want <= %d", len(got), maxDiscordContentLen)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("got = %q, want it to mention truncation", got)
	}
	if !strings.Contains(got, "running") {
		t.Errorf("got = %q, want it to still contain the running marker", got)
	}
}
