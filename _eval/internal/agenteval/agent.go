// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import "context"

const agentEffort = "medium"

// Runner executes one coding-agent session against a prepared workspace.
//
// Implementations shell out to a real agent CLI rather than reimplementing an
// agent, because the thing under test is how that CLI behaves when the repo it
// is pointed at changes.
type Runner interface {
	// Name identifies the agent in experiment tags, e.g. "claude".
	Name() string
	// Model is the pinned model. Results must never be compared across models, so
	// this is recorded on every run.
	Model() string
	// Effort is the pinned reasoning effort.
	Effort() string
	// Version is the agent CLI version. An upgrade between the two sides of a
	// comparison invalidates it, so this is recorded too.
	Version(ctx context.Context) string
	// Run executes the session. It returns a result for any session that started,
	// including a failed one; err is reserved for the harness being unable to run
	// the agent at all.
	Run(ctx context.Context, workspace, prompt, artifactDir string) (*RunResult, error)
}

// ToolCall is one tool invocation observed in the agent's transcript.
type ToolCall struct {
	Name  string
	Input map[string]any
}

func (t ToolCall) str(key string) string {
	if v, ok := t.Input[key].(string); ok {
		return v
	}
	return ""
}

// RunResult is the agent-agnostic view of one session.
type RunResult struct {
	FinalMessage string
	ExitCode     int
	IsError      bool
	TimedOut     bool

	Turns int
	// PermissionDenials counts tool calls the CLI refused. A non-zero value means
	// the harness is misconfigured rather than the agent being wrong, so it is
	// scored as its own criterion instead of being folded into a failure.
	PermissionDenials int

	CostUSD               float64
	DurationMillis        int64
	InputTokens           int64
	OutputTokens          int64
	CachedInputTokens     int64
	CacheWriteInputTokens int64
	ReasoningOutputTokens int64
	TokenCount            int64

	ToolCalls []ToolCall

	TranscriptPath string
	StderrPath     string
}

// ReadPaths returns the file paths the agent read or searched. Paths are as the
// CLI reported them, which for most tools means absolute workspace paths.
func (r *RunResult) ReadPaths() []string {
	var out []string
	for _, tc := range r.ToolCalls {
		switch tc.Name {
		case "Read", "NotebookRead":
			if p := tc.str("file_path"); p != "" {
				out = append(out, p)
			}
		case "Grep", "Glob":
			// A search reports the directory it ran in, plus the pattern. Both are
			// worth keeping: grepping for "INTEGRATIONS" counts as consulting the docs.
			if p := tc.str("path"); p != "" {
				out = append(out, p)
			}
			if p := tc.str("pattern"); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// ShellCommands returns the shell commands the agent ran.
func (r *RunResult) ShellCommands() []string {
	var out []string
	for _, tc := range r.ToolCalls {
		if tc.Name == "Bash" {
			if c := tc.str("command"); c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

// FetchTargets returns anything the agent pulled from the network. Used to detect
// an agent looking up the reference implementation instead of writing it.
func (r *RunResult) FetchTargets() []string {
	var out []string
	for _, tc := range r.ToolCalls {
		switch tc.Name {
		case "WebFetch":
			if u := tc.str("url"); u != "" {
				out = append(out, u)
			}
		case "WebSearch":
			if q := tc.str("query"); q != "" {
				out = append(out, q)
			}
		}
	}
	return out
}
