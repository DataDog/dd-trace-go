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
	if err != nil {
		t.Fatal(err)
	}
	copy := prompt.Template()
	copy.Messages[1].Placeholder.Name = "mutated"
	history := []map[string]any{
		{
			"role": "assistant", "content": "{{ opaque }}",
			"tool_call_id": "call-1", "type": "reasoning",
		},
		{
			"role":    "assistant",
			"content": nil,
			"tool_calls": []any{
				map[string]any{"name": "lookup", "arguments": map[string]any{"id": 1}, "tool_id": "call-1"},
			},
		},
		{
			"role": "tool",
			"tool_results": []any{
				map[string]any{"name": "lookup", "result": "found", "tool_id": "call-1"},
			},
		},
	}
	rendered, err := prompt.Format(map[string]any{
		"plan": "pro", "question": "Why?", "history": history, "empty": []map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"role": "system", "content": "Plan: pro"},
		history[0],
		history[1],
		history[2],
		{"role": "user", "content": "Why?"},
		history[0],
		history[1],
		history[2],
	}
	gotJSON, err := json.Marshal(rendered.Messages)
	require.NoError(t, err)
	wantJSON, err := json.Marshal(want)
	require.NoError(t, err)
	require.JSONEq(t, string(wantJSON), string(gotJSON))
	history[0]["tool_call_id"] = "changed"
	if rendered.Messages[1].ToolCallID == nil || *rendered.Messages[1].ToolCallID != "call-1" {
		t.Fatal("formatted messages alias runtime input")
	}
	annotation := prompt.Annotation(map[string]any{
		"plan": "pro", "question": "Why?", "history": history, "empty": []map[string]any{},
	})
	if !reflect.DeepEqual(annotation.Variables, map[string]string{"plan": "pro", "question": "Why?"}) {
		t.Fatalf("annotation variables %#v", annotation.Variables)
	}
	if annotation.ChatTemplate != nil || !reflect.DeepEqual(annotation.ChatTemplateItems, prompt.Template().Messages) {
		t.Fatalf("annotation does not expose the complete template: %#v", annotation)
	}
	annotation.ChatTemplateItems[1].Placeholder.Name = "custom-history"
	if prompt.Template().Messages[1].Placeholder.Name != "history" {
		t.Fatal("editing annotation changed the cached template")
	}
	annotationJSON, err := json.Marshal(annotation)
	if err != nil {
		t.Fatal(err)
	}
	var restoredAnnotation Prompt
	if err := json.Unmarshal(annotationJSON, &restoredAnnotation); err != nil || !reflect.DeepEqual(restoredAnnotation, annotation) {
		t.Fatalf("annotation JSON lost template data: %s, err %v", annotationJSON, err)
	}
	if _, err := prompt.Format(map[string]any{"empty": []map[string]any{}}); err == nil {
		t.Fatal("expected missing history error")
	}
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
		if _, err := prompt.Format(map[string]any{"history": malformed, "empty": []map[string]any{}}); err == nil {
			t.Fatalf("accepted malformed history %#v", malformed)
		}
	}
	for _, raw := range []string{
		`{"content":null,"role":"assistant","tool_calls":[{"arguments":{"id":1},"name":"lookup","tool_id":"call-1"}]}`,
		`{"role":"assistant","tool_calls":[{"function":{"arguments":"{}","name":"lookup"},"id":"call-1","type":"function"}]}`,
	} {
		var message map[string]any
		if err := json.Unmarshal([]byte(raw), &message); err != nil {
			t.Fatal(err)
		}
		formatted, err := prompt.Format(map[string]any{"history": []map[string]any{message}, "empty": []map[string]any{}})
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := json.Marshal(formatted.Messages[1])
		if err != nil || string(roundTrip) != raw {
			t.Fatalf("tool message round trip: %s, err %v", roundTrip, err)
		}
		if _, err := parsePrompt([]byte(`{"prompt_id":"chat","version":1,"template":[`+raw+`]}`), PromptSourceRegistry); err == nil {
			t.Fatal("runtime tool message accepted as authored template")
		}
	}
	cached, err := json.Marshal(prompt.Template())
	if err != nil {
		t.Fatal(err)
	}
	var restored PromptTemplate
	if err := json.Unmarshal(cached, &restored); err != nil || !reflect.DeepEqual(restored, prompt.Template()) {
		t.Fatalf("restored template %#v, err %v", restored, err)
	}
	for _, item := range []ChatTemplateItem{
		{},
		{Message: &ChatMessage{}, Placeholder: &MessagePlaceholder{Name: "history"}},
		{Placeholder: &MessagePlaceholder{}},
	} {
		if _, err := newManagedPrompt("chat", "1", PromptSourceFallback, PromptTemplate{Messages: []ChatTemplateItem{item}}, "", ""); err == nil {
			t.Fatal("accepted invalid authored union")
		}
	}
}

func TestFormattedPromptMessages(t *testing.T) {
	const raw = `[
		{"role":"user","content":"","tool_calls":null,"tool_results":[],"tool_call_id":null,"name":null},
		{"role":"assistant","content":null,"tool_calls":[{"id":null,"type":"function","function":{"name":"lookup","arguments":"{}","strict":true,"Name":"opaque"},"tool_id":null,"provider":{"version":1}}]},
		{"role":"assistant","tool_calls":[{"name":"lookup","arguments":{"id":1},"tool_id":"","function":null,"provider":null}]},
		{"role":"tool","tool_results":[{"result":null,"name":null,"provider":{"empty":[]}}]},
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
	require.JSONEq(t, raw, string(encoded))
	require.Equal(t, ContentText, formatted.Messages[0].ContentState)
	require.Equal(t, ContentNull, formatted.Messages[1].ContentState)
	require.Equal(t, ContentAbsent, formatted.Messages[2].ContentState)
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
		`{"role":"assistant","tool_calls":[123]}`,
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
	for _, message := range []FormattedMessage{
		{Role: "user", Content: "text", ContentState: ContentNull},
		{Role: "user", ContentState: ContentState(99)},
		{Role: "user", ExtraFields: map[string]json.RawMessage{"content": json.RawMessage(`"shadow"`)}},
		{Role: "user", NullFields: []string{"role"}},
		{Role: "user", ToolCalls: []FormattedToolCall{}, NullFields: []string{"tool_calls"}},
		{Role: "user", ToolCalls: []FormattedToolCall{{Function: &ToolFunction{
			Name: "lookup", Arguments: "{}", ExtraFields: map[string]json.RawMessage{"name": json.RawMessage(`"shadow"`)},
		}}}},
	} {
		_, err := prompt.Format(map[string]any{"history": []FormattedMessage{message}})
		require.Error(t, err)
	}
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
	} {
		if _, err := parsePrompt([]byte(raw), PromptSourceRegistry); err == nil {
			t.Fatalf("accepted malformed response %s", raw)
		}
	}
}
