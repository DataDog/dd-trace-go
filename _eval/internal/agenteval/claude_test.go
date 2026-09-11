// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"strings"
	"testing"
)

// transcriptFixture mirrors the event shapes Claude Code 2.x emits with
// --output-format stream-json: system events, assistant events whose message
// content carries tool_use blocks, and one terminal result event.
const transcriptFixture = `{"type":"system","subtype":"init","session_id":"abc"}
{"type":"system","subtype":"thinking_tokens"}
{"type":"assistant","session_id":"abc","message":{"content":[{"type":"thinking","thinking":"considering"},{"type":"tool_use","name":"Read","input":{"file_path":"/ws/contrib/ORCHESTRION.md"}}]}}
{"type":"assistant","session_id":"abc","message":{"content":[{"type":"tool_use","name":"Grep","input":{"pattern":"orchestrion.yml","path":"/ws/contrib"}}]}}
{"type":"user","session_id":"abc","message":{"content":[{"type":"tool_result","content":"..."}]}}
{"type":"assistant","session_id":"abc","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go build ./..."}},{"type":"tool_use","name":"WebFetch","input":{"url":"https://pkg.go.dev/github.com/twmb/franz-go"}}]}}
{"type":"assistant","session_id":"abc","message":{"content":[{"type":"text","text":"done"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"restored the aspect file","total_cost_usd":0.42,"duration_ms":8203,"num_turns":4,"permission_denials":[{"tool":"Write"}],"usage":{"input_tokens":100,"cache_creation_input_tokens":20,"cache_read_input_tokens":300,"output_tokens":40}}`

func TestParseClaudeTranscript(t *testing.T) {
	res, err := parseClaudeTranscript(strings.NewReader(transcriptFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if res.FinalMessage != "restored the aspect file" {
		t.Errorf("FinalMessage = %q", res.FinalMessage)
	}
	if res.IsError {
		t.Error("IsError = true, want false")
	}
	if res.CostUSD != 0.42 {
		t.Errorf("CostUSD = %v, want 0.42", res.CostUSD)
	}
	if res.DurationMillis != 8203 {
		t.Errorf("DurationMillis = %d, want 8203", res.DurationMillis)
	}
	if res.Turns != 4 {
		t.Errorf("Turns = %d, want 4", res.Turns)
	}
	if res.PermissionDenials != 1 {
		t.Errorf("PermissionDenials = %d, want 1", res.PermissionDenials)
	}
	if res.InputTokens != 420 || res.CachedInputTokens != 300 || res.CacheWriteInputTokens != 20 || res.OutputTokens != 40 || res.TokenCount != 460 {
		t.Errorf("usage = %+v", res)
	}
	if got, want := len(res.ToolCalls), 4; got != want {
		t.Fatalf("ToolCalls = %d, want %d", got, want)
	}

	readPaths := res.ReadPaths()
	if !contains(readPaths, "/ws/contrib/ORCHESTRION.md") {
		t.Errorf("ReadPaths = %v, missing the Read file_path", readPaths)
	}
	// A Grep counts as consulting the docs, so both its path and pattern surface.
	if !contains(readPaths, "orchestrion.yml") || !contains(readPaths, "/ws/contrib") {
		t.Errorf("ReadPaths = %v, missing Grep pattern or path", readPaths)
	}
	if got := res.ShellCommands(); len(got) != 1 || got[0] != "go build ./..." {
		t.Errorf("ShellCommands = %v", got)
	}
	if got := res.FetchTargets(); len(got) != 1 || !strings.Contains(got[0], "franz-go") {
		t.Errorf("FetchTargets = %v", got)
	}
}

func TestParseClaudeTranscriptTolerance(t *testing.T) {
	// A plain-text warning line, a malformed event, and an unknown event type must
	// not discard the run: these transcripts represent hours of work.
	input := "Warning: no stdin data received\n" +
		"{not json at all\n" +
		`{"type":"future_event_type","payload":1}` + "\n" +
		`{"type":"result","subtype":"success","result":"ok","num_turns":1}`

	res, err := parseClaudeTranscript(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.FinalMessage != "ok" {
		t.Errorf("FinalMessage = %q, want the result event to still be read", res.FinalMessage)
	}
}

func TestParseClaudeTranscriptNoResultEvent(t *testing.T) {
	// A killed session has no result event. Parsing must succeed so the caller can
	// still record the timeout rather than losing the whole attempt.
	res, err := parseClaudeTranscript(strings.NewReader(
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/ws/a.go"}}]}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.FinalMessage != "" || res.Turns != 0 {
		t.Errorf("unexpected result fields: %+v", res)
	}
	if len(res.ToolCalls) != 1 {
		t.Errorf("ToolCalls = %d, want 1", len(res.ToolCalls))
	}
}

func TestParseClaudeTranscriptLongLine(t *testing.T) {
	// A single event embeds whole file contents, so it can exceed bufio.Scanner's
	// default 64 KiB token limit.
	big := strings.Repeat("x", 200<<10)
	input := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"/ws/big.go","content":"` + big + `"}}]}}` + "\n" +
		`{"type":"result","result":"ok"}`

	res, err := parseClaudeTranscript(strings.NewReader(input))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(res.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %d, want 1: the long line was dropped", len(res.ToolCalls))
	}
	if res.FinalMessage != "ok" {
		t.Errorf("FinalMessage = %q", res.FinalMessage)
	}
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
