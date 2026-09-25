// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormattedMessages(t *testing.T) {
	const raw = `[
		{"role":"user","content":"","tool_calls":null,"tool_results":[],"tool_call_id":null,"name":null},
		{"role":"assistant","content":null,"tool_calls":[{"id":null,"type":"function","function":{"name":"lookup","arguments":"{}","strict":true,"Name":"opaque"},"tool_id":null,"provider":{"version":1}}]},
		{"role":"assistant","content":"","tool_calls":[{"name":"lookup","arguments":{"id":1},"tool_id":"","function":null,"provider":null}]},
		{"role":"tool","tool_results":[{"result":null,"name":null,"provider":{"empty":[]}}]},
		{"role":"assistant","tool_calls":[{"custom":{"name":"opaque","input":"{{ untouched }}"}}]}
	]`
	const normalized = `[
		{"role":"user","content":"","tool_results":[],"name":null},
		{"role":"assistant","tool_calls":[{"type":"function","function":{"name":"lookup","arguments":"{}","strict":true,"Name":"opaque"},"provider":{"version":1}}]},
		{"role":"assistant","tool_calls":[{"name":"lookup","arguments":{"id":1},"provider":null}]},
		{"role":"tool","tool_results":[{"result":null,"provider":{"empty":[]}}]},
		{"role":"assistant","tool_calls":[{"custom":{"name":"opaque","input":"{{ untouched }}"}}]}
	]`
	var messages []FormattedMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &messages))
	encoded, err := json.Marshal(messages)
	require.NoError(t, err)
	require.JSONEq(t, normalized, string(encoded))
	require.Equal(t, "lookup", messages[1].ToolCalls[0].Function.Name)

	messages[0].Content = "Edited"
	messages[1].ToolCalls[0].Function.Name = "edited_lookup"
	messages[2].ToolCalls[0].Arguments = json.RawMessage(`{"id":2}`)
	messages[3].ToolResults[0].Result = json.RawMessage(`{"found":true}`)
	encoded, err = json.Marshal(messages)
	require.NoError(t, err)
	var decoded []map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, "Edited", decoded[0]["content"])
	require.Equal(t, "edited_lookup", decoded[1]["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)["name"])
	require.Equal(t, map[string]any{"id": float64(2)}, decoded[2]["tool_calls"].([]any)[0].(map[string]any)["arguments"])
	require.Equal(t, map[string]any{"found": true}, decoded[3]["tool_results"].([]any)[0].(map[string]any)["result"])

	for _, raw := range []string{
		`{"role":"assistant","tool_calls":[null]}`,
		`{"role":"assistant","tool_calls":[{"function":{"name":"lookup"}}]}`,
		`{"role":"assistant","tool_calls":[{"function":{"name":null,"arguments":"{}"}}]}`,
		`{"role":"assistant","tool_calls":[{"name":42}]}`,
		`{"role":"tool","tool_results":["bad"]}`,
	} {
		var message FormattedMessage
		require.Error(t, json.Unmarshal([]byte(raw), &message), raw)
	}
	_, err = json.Marshal([]FormattedMessage{
		{Role: "user", ExtraFields: map[string]json.RawMessage{"content": json.RawMessage(`"shadow"`)}},
	})
	require.Error(t, err)
}
