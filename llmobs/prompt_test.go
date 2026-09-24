// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPromptTextAndChat(t *testing.T) {
	text, err := parsePrompt([]byte(`{"prompt_id":"greeting","user_version":"v1","template":"Hello {name}; {{ missing }}","prompt_uuid":"p","prompt_version_uuid":"v"}`), PromptSourceRegistry)
	if err != nil {
		t.Fatal(err)
	}
	renderedText, err := text.Format(map[string]any{"name": 42})
	if err != nil {
		t.Fatal(err)
	}
	if got := renderedText.Text; got != "Hello 42; {{ missing }}" {
		t.Fatalf("rendered %q", got)
	}
	annotation := text.Annotation(map[string]any{"name": 42})
	if annotation.Template != "Hello {name}; {{ missing }}" || annotation.Variables["name"] != "42" || annotation.PromptUUID != "p" || annotation.PromptVersionUUID != "v" {
		t.Fatalf("annotation %#v", annotation)
	}

	chat, err := parsePrompt(map[string]any{"prompt_id": "chat", "version": 2, "template": []any{map[string]any{"role": "", "content": "Hi {{ name }}"}}}, PromptSourceFeatureFlag)
	if err != nil {
		t.Fatal(err)
	}
	original := chat.Template()
	original.Messages[0].Message.Content = "mutated"
	if got := chat.Template().Messages[0].Message.Content; got != "Hi {{ name }}" {
		t.Fatalf("cached prompt mutated: %q", got)
	}
	rendered, err := chat.Format(map[string]any{"name": "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rendered.Messages, []FormattedMessage{{Role: "", Content: "Hi Ada"}}) {
		t.Fatalf("rendered %#v", rendered)
	}
	if chat.Version() != "2" || chat.Source() != PromptSourceFeatureFlag || chat.ID() != "chat" {
		t.Fatalf("metadata: %#v", chat)
	}
	if _, err := newManagedPrompt("bad", "1", PromptSourceFallback, PromptTemplate{Text: "x", Messages: []ChatTemplateItem{}}, "", ""); err == nil {
		t.Fatal("expected ambiguous template error")
	}
}

func TestPromptMessagePlaceholders(t *testing.T) {
	prompt, err := parsePrompt([]byte(`{
		"prompt_id":"chat","version":3,
		"template":[
			{"role":"system","content":"Plan: {{ plan }}"},
			{"type":"placeholder","name":"history"},
			{"role":"user","content":"{{ question }}"},
			{"type":"placeholder","name":"history"},
			{"type":"placeholder","name":"empty"}
		]
	}`), PromptSourceRegistry)
	require.NoError(t, err)
	copy := prompt.Template()
	copy.Messages[1].Placeholder.Name = "mutated"
	history := []map[string]any{
		{
			"role": "assistant", "content": "{{ opaque }}",
			"tool_call_id": "call-1", "type": "reasoning",
		},
	}
	rendered, err := prompt.Format(map[string]any{
		"plan": "pro", "question": "Why?", "history": history, "empty": []map[string]any{},
	})
	require.NoError(t, err)
	want := []map[string]any{
		{"role": "system", "content": "Plan: pro"},
		history[0],
		{"role": "user", "content": "Why?"},
		history[0],
	}
	gotJSON, err := json.Marshal(rendered.Messages)
	require.NoError(t, err)
	wantJSON, err := json.Marshal(want)
	require.NoError(t, err)
	require.JSONEq(t, string(wantJSON), string(gotJSON))
	history[0]["tool_call_id"] = "changed"
	require.Equal(t, "call-1", rendered.Messages[1].ToolCallID, "formatted messages alias runtime input")
	annotation := prompt.Annotation(map[string]any{
		"plan": "pro", "question": "Why?", "history": history, "empty": []map[string]any{},
	})
	require.Equal(t, map[string]string{"plan": "pro", "question": "Why?"}, annotation.Variables)
	require.Nil(t, annotation.ChatTemplate)
	require.Equal(t, prompt.Template().Messages, annotation.ChatTemplateItems)
	annotation.ChatTemplateItems[1].Placeholder.Name = "custom-history"
	require.Equal(t, "history", prompt.Template().Messages[1].Placeholder.Name, "editing annotation changed the cached template")
	annotationJSON, err := json.Marshal(annotation)
	require.NoError(t, err)
	var restoredAnnotation Prompt
	require.NoError(t, json.Unmarshal(annotationJSON, &restoredAnnotation))
	require.Equal(t, annotation, restoredAnnotation)
	_, err = prompt.Format(map[string]any{"empty": []map[string]any{}})
	require.Error(t, err)
	for _, malformed := range []any{
		"history",
		[]any{},
		[]map[string]any{nil},
		[]map[string]any{{"role": 1, "content": "hello"}},
		[]map[string]any{{"role": "assistant"}},
		[]map[string]any{{"type": "placeholder", "name": "nested"}},
		[]map[string]any{{"role": "assistant",
			"content": []any{map[string]any{"type": "image"}}, "tool_calls": []any{map[string]any{}},
		}},
	} {
		_, err := prompt.Format(map[string]any{"history": malformed, "empty": []map[string]any{}})
		require.Error(t, err, "accepted malformed history %#v", malformed)
	}
	cached, err := json.Marshal(prompt.Template())
	require.NoError(t, err)
	var restored PromptTemplate
	require.NoError(t, json.Unmarshal(cached, &restored))
	require.Equal(t, prompt.Template(), restored)
	for _, item := range []ChatTemplateItem{
		{},
		{Message: &ChatMessage{}, Placeholder: &MessagePlaceholder{Name: "history"}},
		{Placeholder: &MessagePlaceholder{}},
	} {
		_, err := newManagedPrompt("chat", "1", PromptSourceFallback, PromptTemplate{Messages: []ChatTemplateItem{item}}, "", "")
		require.Error(t, err)
	}
}

func TestFormattedPromptMessages(t *testing.T) {
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
	var history []FormattedMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &history))
	prompt, err := parsePrompt([]byte(`{"prompt_id":"tools","version":1,"template":[{"type":"placeholder","name":"history"}]}`), PromptSourceRegistry)
	require.NoError(t, err)
	formatted, err := prompt.Format(map[string]any{"history": history})
	require.NoError(t, err)
	encoded, err := json.Marshal(formatted.Messages)
	require.NoError(t, err)
	require.JSONEq(t, normalized, string(encoded))
	require.Equal(t, "lookup", formatted.Messages[1].ToolCalls[0].Function.Name)

	formatted.Messages[0].Content = "Edited"
	formatted.Messages[1].ToolCalls[0].Function.Name = "edited_lookup"
	formatted.Messages[2].ToolCalls[0].Arguments = json.RawMessage(`{"id":2}`)
	formatted.Messages[3].ToolResults[0].Result = json.RawMessage(`{"found":true}`)
	encoded, err = json.Marshal(formatted.Messages)
	require.NoError(t, err)
	var decoded []map[string]any
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, "Edited", decoded[0]["content"])
	require.Equal(t, "edited_lookup", decoded[1]["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)["name"])
	require.Equal(t, map[string]any{"id": float64(2)}, decoded[2]["tool_calls"].([]any)[0].(map[string]any)["arguments"])
	require.Equal(t, map[string]any{"found": true}, decoded[3]["tool_results"].([]any)[0].(map[string]any)["result"])
	require.Equal(t, "lookup", history[1].ToolCalls[0].Function.Name, "formatting must not alias typed input")

	for _, raw := range []string{
		`{"role":"assistant","tool_calls":[null]}`,
		`{"role":"assistant","tool_calls":[{"function":{"name":"lookup"}}]}`,
		`{"role":"assistant","tool_calls":[{"function":{"name":null,"arguments":"{}"}}]}`,
		`{"role":"assistant","tool_calls":[{"name":42}]}`,
		`{"role":"tool","tool_results":["bad"]}`,
	} {
		var message map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &message))
		_, err := prompt.Format(map[string]any{"history": []map[string]any{message}})
		require.Error(t, err, raw)
	}
	_, err = prompt.Format(map[string]any{"history": []FormattedMessage{
		{Role: "user", ExtraFields: map[string]json.RawMessage{"content": json.RawMessage(`"shadow"`)}},
	}})
	require.Error(t, err)
}
func TestPromptFormatBalancedPlaceholders(t *testing.T) {
	prompt, err := newManagedPrompt("balanced", "1", PromptSourceRegistry, PromptTemplate{
		Text: `{{double}} {single} | {{double} | {single}} | {{{double}}} | JSON: {"age": {age}}`,
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := prompt.Format(map[string]any{"double": "two", "single": "one", "age": 42})
	if err != nil {
		t.Fatal(err)
	}
	if want := `two one | {{double} | {single}} | {{{double}}} | JSON: {"age": {age}}`; rendered.Text != want {
		t.Fatalf("rendered %q, want %q", rendered.Text, want)
	}
}

func TestPromptEmptyChatAndVersionUUIDFallback(t *testing.T) {
	prompt, err := parsePrompt([]byte(`{"prompt_id":"empty","version":1,"ID":"version-id"}`), PromptSourceRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if prompt.Template().Messages == nil || len(prompt.Template().Messages) != 0 {
		t.Fatalf("expected non-nil empty chat: %#v", prompt.Template())
	}
	if prompt.Annotation(nil).PromptVersionUUID != "version-id" {
		t.Fatal("missing version UUID fallback")
	}
	withoutUUID, err := parsePrompt([]byte(`{"prompt_id":"empty","version":1}`), PromptSourceRegistry)
	if err != nil || withoutUUID.Annotation(nil).PromptVersionUUID != "" {
		t.Fatalf("unexpected version UUID fallback: prompt=%#v err=%v", withoutUUID, err)
	}
}

func TestPromptPrefersChatToEmptyText(t *testing.T) {
	prompt, err := parsePrompt([]byte(`{"prompt_id":"chat","version":1,"template":"","chat_template":[{"role":"user","content":"hello"}]}`), PromptSourceRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prompt.Template().Messages, []ChatTemplateItem{{Message: &ChatMessage{Role: "user", Content: "hello"}}}) {
		t.Fatalf("template %#v", prompt.Template())
	}
}

func TestPromptRejectsMalformedResponses(t *testing.T) {
	for _, raw := range []string{
		`[]`,
		`{}`,
		`{"prompt_id":"p","version":0}`,
		`{"prompt_id":"p","version":1,"template":42}`,
		`{"prompt_id":"p","version":1,"template":"text","chat_template":[]}`,
		`{"prompt_id":"p","version":1,"template":"text"} trailing`,
		`{"prompt_id":"p","version":1,"template":[{"role":"assistant","content":null,"tool_calls":[{"name":"lookup","arguments":{}}]}]}`,
		`{"prompt_id":"p","version":1,"template":[{"role":"assistant","tool_calls":[{"function":{"name":"lookup","arguments":"{}"}}]}]}`,
	} {
		if _, err := parsePrompt([]byte(raw), PromptSourceRegistry); err == nil {
			t.Fatalf("accepted malformed response %s", raw)
		}
	}
}
