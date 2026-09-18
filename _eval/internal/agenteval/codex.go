// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/moby/moby/api/types/mount"
)

// CodexRunner drives Codex in non-interactive mode inside an isolated container.
type CodexRunner struct {
	Image              string
	ModelName          string
	ContainerResources ContainerResources

	versionOnce sync.Once
	version     string
}

func (r *CodexRunner) Name() string   { return "codex" }
func (r *CodexRunner) Model() string  { return r.ModelName }
func (r *CodexRunner) Effort() string { return agentEffort }

func (r *CodexRunner) Version(context.Context) string {
	r.versionOnce.Do(func() { r.version = "container " + r.image() })
	return r.version
}

func (r *CodexRunner) image() string {
	if r.Image != "" {
		return r.Image
	}
	return "dd-trace-go-agent-eval/codex:0.147.0"
}

func (r *CodexRunner) Run(ctx context.Context, workspace, prompt, artifactDir string) (*RunResult, error) {
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, err
	}
	transcriptPath := filepath.Join(artifactDir, "transcript.jsonl")
	stderrPath := filepath.Join(artifactDir, "agent.stderr")

	authPath, err := codexAuthFile()
	if err != nil {
		return nil, err
	}
	args := []string{
		"codex", "--search", "exec", "--json", "--ephemeral",
		"--ignore-user-config", "--ignore-rules",
		"--disable", "memories", "--disable", "plugins", "--disable", "multi_agent",
		"--dangerously-bypass-approvals-and-sandbox",
		"--config", "model_reasoning_effort=" + r.Effort(),
		"--model", r.ModelName, "--cd", containerWorkspace,
		prompt,
	}
	containerRun, err := runInContainer(ctx, workspace, transcriptPath, stderrPath, containerInvocation{
		Name:    agentContainerName("codex", artifactDir),
		Image:   r.image(),
		Command: args,
		Environment: map[string]string{
			"CODEX_HOME": containerHome + "/.codex",
		},
		AuthMounts: []mount.Mount{{
			Type:     mount.TypeBind,
			Source:   authPath,
			Target:   "/run/agent-auth/codex-auth.json",
			ReadOnly: true,
		}},
		Resources: r.ContainerResources,
	})
	if err != nil {
		return nil, err
	}

	transcript, err := os.Open(transcriptPath)
	if err != nil {
		return nil, err
	}
	defer transcript.Close()
	res, err := parseCodexTranscript(transcript)
	if err != nil {
		return nil, fmt.Errorf("parse transcript %s: %w", transcriptPath, err)
	}
	res.TranscriptPath = transcriptPath
	res.StderrPath = stderrPath
	res.ExitCode = containerRun.ExitCode
	res.TimedOut = containerRun.TimedOut
	res.CostUSD = estimateCodexCost(r.ModelName, res)
	if containerRun.TimedOut || containerRun.ExitCode != 0 {
		res.IsError = true
	}
	return res, nil
}

func codexAuthFile() (string, error) {
	if path := os.Getenv("CODEX_AUTH_FILE"); path != "" {
		return requireRegularFile(path, "Codex auth")
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, ".codex")
	}
	return requireRegularFile(filepath.Join(home, "auth.json"), "Codex auth")
}

func requireRegularFile(path, label string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%s file %s: %w", label, abs, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s path %s is not a regular file", label, abs)
	}
	return abs, nil
}

type codexEvent struct {
	Type  string          `json:"type"`
	Item  json.RawMessage `json:"item"`
	Error json.RawMessage `json:"error"`
	Usage struct {
		InputTokens           int64 `json:"input_tokens"`
		CachedInputTokens     int64 `json:"cached_input_tokens"`
		CacheWriteInputTokens int64 `json:"cache_write_input_tokens"`
		OutputTokens          int64 `json:"output_tokens"`
		ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	} `json:"usage"`
}

type codexItem struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Command string `json:"command"`
	Query   string `json:"query"`
	Name    string `json:"name"`
	Tool    string `json:"tool"`
	Server  string `json:"server"`
	Status  string `json:"status"`
}

func parseCodexTranscript(r io.Reader) (*RunResult, error) {
	res := &RunResult{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256<<10), maxTranscriptLine)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "turn.completed":
			res.Turns++
			res.InputTokens += ev.Usage.InputTokens
			res.CachedInputTokens += ev.Usage.CachedInputTokens
			res.CacheWriteInputTokens += ev.Usage.CacheWriteInputTokens
			res.OutputTokens += ev.Usage.OutputTokens
			res.ReasoningOutputTokens += ev.Usage.ReasoningOutputTokens
		case "turn.failed", "error":
			res.IsError = true
		case "item.completed":
			var item codexItem
			if err := json.Unmarshal(ev.Item, &item); err != nil {
				continue
			}
			switch item.Type {
			case "agent_message":
				if item.Text != "" {
					res.FinalMessage = item.Text
				}
			case "command_execution":
				if item.Command != "" {
					res.ToolCalls = append(res.ToolCalls, ToolCall{Name: "Bash", Input: map[string]any{"command": item.Command}})
				}
			case "web_search":
				if item.Query != "" {
					res.ToolCalls = append(res.ToolCalls, ToolCall{Name: "WebSearch", Input: map[string]any{"query": item.Query}})
				}
			case "mcp_tool_call":
				name := strings.Trim(strings.Join([]string{item.Server, item.Tool, item.Name}, "."), ".")
				res.ToolCalls = append(res.ToolCalls, ToolCall{Name: name, Input: map[string]any{}})
			case "file_change":
				res.ToolCalls = append(res.ToolCalls, ToolCall{Name: "Edit", Input: map[string]any{}})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	res.TokenCount = res.InputTokens + res.OutputTokens
	return res, nil
}

// estimateCodexCost uses the published GPT-5.6 Terra token rates. Codex reports
// cached input as a subset of input_tokens, so it is subtracted before applying
// the uncached rate. The long-context multiplier applies to the whole session
// once input exceeds 272K tokens.
func estimateCodexCost(model string, result *RunResult) float64 {
	if model != "gpt-5.6-terra" || result == nil {
		return 0
	}
	uncached := result.InputTokens - result.CachedInputTokens - result.CacheWriteInputTokens
	if uncached < 0 {
		uncached = 0
	}
	inputMultiplier, outputMultiplier := 1.0, 1.0
	if result.InputTokens > 272_000 {
		inputMultiplier = 2
		outputMultiplier = 1.5
	}
	const million = 1_000_000
	return inputMultiplier*(float64(uncached)*2/million+
		float64(result.CachedInputTokens)*0.20/million+
		float64(result.CacheWriteInputTokens)*2.50/million) +
		outputMultiplier*float64(result.OutputTokens)*12/million
}
