// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

// maxCapturedOutput bounds what we keep from a validation command. A failing
// `go test ./...` can emit megabytes, and only the tail is diagnostic.
const maxCapturedOutput = 16 << 10

// RunValidation executes each command in the workspace and records the outcome.
// Commands run through a shell because dataset records hold shell strings, and
// they run sequentially because they share the workspace and the Go build cache.
//
// A non-zero exit is data, not an error: the point of the eval is to count how
// often the agent's work fails to build or test.
func RunValidation(ctx context.Context, workspace string, cmds []ValidationCommand, perCmdTimeout time.Duration) []CmdResult {
	results := make([]CmdResult, 0, len(cmds))
	for _, vc := range cmds {
		results = append(results, runOneCommand(ctx, workspace, vc, perCmdTimeout))
	}
	return results
}

func runOneCommand(ctx context.Context, workspace string, vc ValidationCommand, timeout time.Duration) CmdResult {
	cmdCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		cmdCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", vc.Command)
	cmd.Dir = workspace
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	res := CmdResult{
		Label:   vc.Label,
		Command: vc.Command,
		Stdout:  tailString(stdout.String(), maxCapturedOutput),
		Stderr:  tailString(stderr.String(), maxCapturedOutput),
	}
	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.Is(cmdCtx.Err(), context.DeadlineExceeded):
		res.TimedOut = true
		res.ExitCode = -1
	default:
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			res.ExitCode = -1
			res.Stderr = tailString(res.Stderr+"\n"+err.Error(), maxCapturedOutput)
		}
	}
	return res
}

// tailString keeps the last max bytes, which is where command failures explain
// themselves.
func tailString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "...[truncated]...\n" + s[len(s)-max:]
}

// headString keeps the first max bytes, for content whose start is the useful part.
func headString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n...[truncated]..."
}
