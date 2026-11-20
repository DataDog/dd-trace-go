// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ClaudeRunner drives Claude Code in headless mode.
type ClaudeRunner struct {
	// Image contains Claude Code and the repository toolchain.
	Image string
	// ModelName pins the model. Leaving it empty lets the CLI choose, which makes
	// runs incomparable, so callers should always set it.
	ModelName string
	// AllowedTools is passed through to --tools. Empty uses the coding tools below.
	AllowedTools []string
	// ContainerResources sets per-session CPU and memory limits.
	ContainerResources ContainerResources

	versionOnce sync.Once
	version     string
}

// Name implements Runner.
func (r *ClaudeRunner) Name() string { return "claude" }

// Model implements Runner.
func (r *ClaudeRunner) Model() string { return r.ModelName }

// Effort implements Runner.
func (r *ClaudeRunner) Effort() string { return agentEffort }

// Version implements Runner.
func (r *ClaudeRunner) Version(ctx context.Context) string {
	r.versionOnce.Do(func() {
		r.version = "container " + r.image()
	})
	return r.version
}

func (r *ClaudeRunner) image() string {
	if r.Image != "" {
		return r.Image
	}
	return "dd-trace-go-agent-eval/claude:2.1.231"
}

// Run implements Runner.
func (r *ClaudeRunner) Run(ctx context.Context, workspace, prompt, artifactDir string) (*RunResult, error) {
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, err
	}
	transcriptPath := filepath.Join(artifactDir, "transcript.jsonl")
	stderrPath := filepath.Join(artifactDir, "agent.stderr")

	args := []string{
		"claude", "-p", prompt,
		"--output-format", "stream-json", "--verbose",
		"--safe-mode", "--no-session-persistence",
		"--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--disable-slash-commands", "--dangerously-skip-permissions",
		"--effort", r.Effort(),
	}
	if r.ModelName != "" {
		args = append(args, "--model", r.ModelName)
	}
	tools := r.AllowedTools
	if len(tools) == 0 {
		tools = []string{"Bash", "Read", "Edit", "Write", "Glob", "Grep", "WebFetch", "WebSearch"}
	}
	args = append(args, "--tools", strings.Join(tools, ","))

	authEnv, err := claudeContainerAuth(ctx)
	if err != nil {
		return nil, err
	}
	containerRun, err := runInContainer(ctx, workspace, transcriptPath, stderrPath, containerInvocation{
		Name:        agentContainerName("claude", artifactDir),
		Image:       r.image(),
		Command:     args,
		Environment: authEnv,
		Resources:   r.ContainerResources,
	})
	if err != nil {
		return nil, err
	}

	transcript, err := os.Open(transcriptPath)
	if err != nil {
		return nil, err
	}
	defer transcript.Close()
	res, parseErr := parseClaudeTranscript(transcript)
	if parseErr != nil {
		return nil, fmt.Errorf("parse transcript %s: %w", transcriptPath, parseErr)
	}
	res.TranscriptPath = transcriptPath
	res.StderrPath = stderrPath

	res.ExitCode = containerRun.ExitCode
	res.TimedOut = containerRun.TimedOut
	if containerRun.TimedOut || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.IsError = true
	}
	return res, nil
}

// claudeEvent covers the subset of the stream-json schema this harness reads.
// Unknown fields and event types are ignored so a CLI upgrade that adds events
// does not break parsing.
type claudeEvent struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`

	Message struct {
		Content []struct {
			Type  string         `json:"type"`
			Name  string         `json:"name"`
			Input map[string]any `json:"input"`
		} `json:"content"`
	} `json:"message"`

	// Fields below are only present on the terminal "result" event.
	Result            string            `json:"result"`
	IsError           bool              `json:"is_error"`
	TotalCostUSD      float64           `json:"total_cost_usd"`
	DurationMS        int64             `json:"duration_ms"`
	NumTurns          int               `json:"num_turns"`
	PermissionDenials []json.RawMessage `json:"permission_denials"`
	Usage             struct {
		InputTokens              int64 `json:"input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
	} `json:"usage"`
}

// maxTranscriptLine bounds one JSONL event. A single event embeds whole file
// contents, so the default scanner limit of 64 KiB is far too small.
const maxTranscriptLine = 64 << 20

func parseClaudeTranscript(r io.Reader) (*RunResult, error) {
	res := &RunResult{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256<<10), maxTranscriptLine)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			// The CLI occasionally emits plain-text warnings on stdout.
			continue
		}
		var ev claudeEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// One malformed event should not discard a multi-hour run.
			continue
		}
		switch ev.Type {
		case "assistant":
			for _, block := range ev.Message.Content {
				if block.Type == "tool_use" {
					res.ToolCalls = append(res.ToolCalls, ToolCall{Name: block.Name, Input: block.Input})
				}
			}
		case "result":
			res.FinalMessage = ev.Result
			res.IsError = ev.IsError
			res.CostUSD = ev.TotalCostUSD
			res.DurationMillis = ev.DurationMS
			res.Turns = ev.NumTurns
			res.PermissionDenials = len(ev.PermissionDenials)
			res.InputTokens = ev.Usage.InputTokens + ev.Usage.CacheCreationInputTokens + ev.Usage.CacheReadInputTokens
			res.CachedInputTokens = ev.Usage.CacheReadInputTokens
			res.CacheWriteInputTokens = ev.Usage.CacheCreationInputTokens
			res.OutputTokens = ev.Usage.OutputTokens
			res.TokenCount = res.InputTokens + res.OutputTokens
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return res, nil
}
