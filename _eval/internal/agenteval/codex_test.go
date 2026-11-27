// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"strings"
	"testing"
)

const codexTranscriptFixture = `{"type":"thread.started","thread_id":"thread-1"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item-1","type":"command_execution","command":"sed -n '1,80p' contrib/INTEGRATIONS.md","status":"completed","exit_code":0}}
{"type":"item.completed","item":{"id":"item-2","type":"web_search","query":"dd-trace-go upstream implementation"}}
{"type":"item.completed","item":{"id":"item-3","type":"file_change","changes":[{"path":"contrib/example.go","kind":"update"}],"status":"completed"}}
{"type":"item.completed","item":{"id":"item-4","type":"agent_message","text":"implemented the integration"}}
{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":20,"cache_write_input_tokens":5,"output_tokens":40,"reasoning_output_tokens":10}}
`

func TestParseCodexTranscript(t *testing.T) {
	res, err := parseCodexTranscript(strings.NewReader(codexTranscriptFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.FinalMessage != "implemented the integration" {
		t.Errorf("FinalMessage = %q", res.FinalMessage)
	}
	if res.Turns != 1 {
		t.Errorf("Turns = %d, want 1", res.Turns)
	}
	if res.InputTokens != 100 || res.CachedInputTokens != 20 || res.CacheWriteInputTokens != 5 || res.OutputTokens != 40 || res.TokenCount != 140 {
		t.Errorf("usage = %+v", res)
	}
	if got := res.ShellCommands(); len(got) != 1 || !strings.Contains(got[0], "INTEGRATIONS.md") {
		t.Errorf("ShellCommands = %v", got)
	}
	if got := res.FetchTargets(); len(got) != 1 || !strings.Contains(got[0], "upstream") {
		t.Errorf("FetchTargets = %v", got)
	}
	if got, want := len(res.ToolCalls), 3; got != want {
		t.Errorf("ToolCalls = %d, want %d", got, want)
	}
}

func TestEstimateCodexCost(t *testing.T) {
	result := &RunResult{InputTokens: 100_000, CachedInputTokens: 20_000, CacheWriteInputTokens: 10_000, OutputTokens: 5_000}
	if got, want := estimateCodexCost("gpt-5.6-terra", result), 0.229; got != want {
		t.Errorf("cost = %v, want %v", got, want)
	}
	if got := estimateCodexCost("another-model", result); got != 0 {
		t.Errorf("unknown model cost = %v, want 0", got)
	}
}

func TestParseCodexTranscriptTolerance(t *testing.T) {
	input := "warning from the CLI\n{not-json\n" +
		`{"type":"future.event","value":1}` + "\n" +
		`{"type":"turn.failed","error":{"message":"failed"}}`
	res, err := parseCodexTranscript(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !res.IsError {
		t.Error("IsError = false, want true")
	}
}
