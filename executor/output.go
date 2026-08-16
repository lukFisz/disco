package executor

import (
	"fmt"
	"strings"

	"disco/config"
)

// maxDiscordContentLen is the maximum length of formatted content this
// package will produce, leaving headroom under Discord's 2000-char message
// cap for the caller to add its own framing.
const maxDiscordContentLen = 1900

const truncatedMarker = "\n... output truncated, see attached file ...\n"

// Formatted is a message ready to be sent by a later "discord" package.
type Formatted struct {
	Content    string      // always <= maxDiscordContentLen
	Attachment *Attachment // non-nil only when full output didn't fit inline
}

// Attachment is a file to be uploaded alongside a Formatted message.
type Attachment struct {
	Filename string
	Data     []byte
}

// Format builds the FINAL message for a completed (or timed-out) command.
func Format(cmdName string, mode config.OutputMode, res *Result) Formatted {
	if mode == config.OutputSilent {
		content := fmt.Sprintf("Exit code: %d", res.ExitCode)
		if res.TimedOut {
			content += fmt.Sprintf(" (timed out after %s)", res.Duration)
		}
		return Formatted{Content: content}
	}

	combined := combineOutput(res.Stdout, res.Stderr)

	header := fmt.Sprintf("Exit code: %d", res.ExitCode)
	if res.TimedOut {
		header += fmt.Sprintf(" (timed out after %s)", res.Duration)
	}

	full := buildFenced(header, combined)

	if len(full) <= maxDiscordContentLen {
		return Formatted{Content: full}
	}

	// Doesn't fit: truncate and attach the full output as a file.
	truncated := truncateForBudget(header, combined)
	return Formatted{
		Content: truncated,
		Attachment: &Attachment{
			Filename: cmdName + "-output.txt",
			Data:     []byte(combined),
		},
	}
}

// FormatProgress builds an INTERMEDIATE stream-mode update (no attachment
// ever - attachments only make sense on the final message).
func FormatProgress(stdout, stderr string) string {
	const suffix = " (running…)"

	combined := combineOutput(stdout, stderr)
	content := buildFenced("", combined) + suffix
	if len(content) <= maxDiscordContentLen {
		return content
	}

	overhead := len("\n```\n") + len("\n```") + len(truncatedMarker) + len(suffix)
	budget := maxDiscordContentLen - overhead
	body := truncateBody(combined, budget)
	return buildFenced("", body+truncatedMarker) + suffix
}

// combineOutput merges stdout/stderr into one string, labeling each section
// when both are present.
func combineOutput(stdout, stderr string) string {
	switch {
	case stdout != "" && stderr != "":
		return "stdout:\n" + stdout + "\nstderr:\n" + stderr
	case stdout != "":
		return stdout
	case stderr != "":
		return stderr
	default:
		return ""
	}
}

// buildFenced wraps body in a fenced code block, preceded by an optional
// header line.
func buildFenced(header, body string) string {
	var sb strings.Builder
	if header != "" {
		sb.WriteString(header)
		sb.WriteString("\n")
	}
	sb.WriteString("```\n")
	sb.WriteString(body)
	sb.WriteString("\n```")
	return sb.String()
}

// truncateForBudget rebuilds the fenced content for Format(), truncating
// the body so the whole thing (header + fences + truncation marker) fits
// within maxDiscordContentLen.
func truncateForBudget(header, combined string) string {
	overhead := len(header) + len("\n```\n") + len("\n```") + len(truncatedMarker)
	budget := maxDiscordContentLen - overhead
	body := truncateBody(combined, budget)
	return buildFenced(header, body+truncatedMarker)
}

// truncateBody returns the first n bytes of s (n clamped to [0, len(s)]).
func truncateBody(s string, n int) string {
	if n < 0 {
		n = 0
	}
	if n > len(s) {
		n = len(s)
	}
	return s[:n]
}
