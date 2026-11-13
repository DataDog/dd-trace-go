// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// A comparison runs one agent session per task, per side, per run, and a single
// session takes tens of minutes. Without progress output the harness looks hung,
// so every stage that can take minutes reports when it starts and what it cost
// when it ends.

// progressf writes one progress line. Callers pass a stage prefix so the output
// groups by phase when the log is read later.
func progressf(format string, args ...any) {
	log.Printf("agent-eval: "+format, args...)
}

// fmtDuration renders a duration for humans: "3m42s" rather than
// "3m42.118283901s".
func fmtDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	if d < time.Minute {
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

// fmtTokens abbreviates a token count, because raw six-digit counts are hard to
// compare at a glance across sessions.
func fmtTokens(n int64) string {
	switch {
	case n <= 0:
		return "0"
	case n < 1_000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	}
}

// usageSummary describes what one agent session consumed.
func usageSummary(run *RunResult) string {
	if run == nil {
		return "no usage reported"
	}
	parts := []string{
		fmt.Sprintf("%d turns", run.Turns),
		fmt.Sprintf("%d tool calls", len(run.ToolCalls)),
		fmt.Sprintf("tokens in=%s out=%s", fmtTokens(run.InputTokens), fmtTokens(run.OutputTokens)),
	}
	if run.CachedInputTokens > 0 || run.CacheWriteInputTokens > 0 {
		parts = append(parts, fmt.Sprintf("cache read=%s write=%s",
			fmtTokens(run.CachedInputTokens), fmtTokens(run.CacheWriteInputTokens)))
	}
	if run.ReasoningOutputTokens > 0 {
		parts = append(parts, "reasoning="+fmtTokens(run.ReasoningOutputTokens))
	}
	if run.TokenCount > 0 {
		parts = append(parts, "total="+fmtTokens(run.TokenCount))
	}
	if run.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("cost=$%.4f", run.CostUSD))
	}
	if run.PermissionDenials > 0 {
		parts = append(parts, fmt.Sprintf("permission denials=%d", run.PermissionDenials))
	}
	return strings.Join(parts, ", ")
}

// outcome renders the pass or fail verdict for a finished session.
func outcome(run *RunResult) string {
	switch {
	case run == nil:
		return "unknown"
	case run.IsError:
		return fmt.Sprintf("ERROR (exit %d)", run.ExitCode)
	case run.ExitCode != 0:
		return fmt.Sprintf("nonzero exit %d", run.ExitCode)
	default:
		return "ok"
	}
}
