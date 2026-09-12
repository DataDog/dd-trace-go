// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"encoding/json"
	"reflect"
	"testing"
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
	original.Messages[0].Content = "mutated"
	if got := chat.Template().Messages[0].Content; got != "Hi {{ name }}" {
		t.Fatalf("cached prompt mutated: %q", got)
	}
	rendered, err := chat.Format(map[string]any{"name": "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rendered.Messages, []PromptMessage{{Role: "", Content: "Hi Ada"}}) {
		t.Fatalf("rendered %#v", rendered)
	}
	if chat.Version() != "2" || chat.Source() != PromptSourceFeatureFlag || chat.ID() != "chat" {
		t.Fatalf("metadata: %#v", chat)
	}
	if _, err := newManagedPrompt("bad", "1", PromptSourceFallback, PromptTemplate{Text: "x", Messages: []PromptMessage{}}, "", ""); err == nil {
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
	history := []PromptMessage{
		{
			Role: "assistant", Content: "{{ opaque }}",
			AdditionalFields: map[string]any{"tool_call_id": "call-1", "type": "reasoning"},
		},
		{
			Role: "assistant",
			AdditionalFields: map[string]any{
				"content": nil,
				"tool_calls": []any{
					map[string]any{"name": "lookup", "arguments": map[string]any{"id": 1}, "tool_id": "call-1"},
				},
			},
		},
		{
			Role: "tool",
			AdditionalFields: map[string]any{
				"tool_results": []any{
					map[string]any{"name": "lookup", "result": "found", "tool_id": "call-1"},
				},
			},
		},
	}
	rendered, err := prompt.Format(map[string]any{
		"plan": "pro", "question": "Why?", "history": history, "empty": []PromptMessage{},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []PromptMessage{
		{Role: "system", Content: "Plan: pro"},
		{Role: "assistant", Content: "{{ opaque }}", AdditionalFields: map[string]any{"tool_call_id": "call-1", "type": "reasoning"}},
		history[1],
		history[2],
		{Role: "user", Content: "Why?"},
		{Role: "assistant", Content: "{{ opaque }}", AdditionalFields: map[string]any{"tool_call_id": "call-1", "type": "reasoning"}},
		history[1],
		history[2],
	}
	if !reflect.DeepEqual(rendered.Messages, want) {
		t.Fatalf("rendered %#v, want %#v", rendered.Messages, want)
	}
	history[0].AdditionalFields["tool_call_id"] = "changed"
	if rendered.Messages[1].AdditionalFields["tool_call_id"] != "call-1" {
		t.Fatal("formatted messages alias runtime input")
	}
	annotation := prompt.Annotation(map[string]any{
		"plan": "pro", "question": "Why?", "history": history, "empty": []PromptMessage{},
	})
	if !reflect.DeepEqual(annotation.Variables, map[string]string{"plan": "pro", "question": "Why?"}) {
		t.Fatalf("annotation variables %#v", annotation.Variables)
	}
	if _, err := prompt.Format(map[string]any{"empty": []PromptMessage{}}); err == nil {
		t.Fatal("expected missing history error")
	}
	for _, malformed := range []any{
		"history",
		[]any{},
		[]PromptMessage{{Type: "placeholder", Name: "nested"}},
		[]PromptMessage{{Role: "assistant", AdditionalFields: map[string]any{
			"content": []any{map[string]any{"type": "image"}}, "tool_calls": []any{map[string]any{}},
		}}},
	} {
		if _, err := prompt.Format(map[string]any{"history": malformed, "empty": []PromptMessage{}}); err == nil {
			t.Fatalf("accepted malformed history %#v", malformed)
		}
	}
	encoded, err := json.Marshal(rendered.Messages[1])
	if err != nil || string(encoded) != `{"content":"{{ opaque }}","role":"assistant","tool_call_id":"call-1","type":"reasoning"}` {
		t.Fatalf("encoded message %s, err %v", encoded, err)
	}
	encoded, err = json.Marshal(rendered.Messages[2])
	if err != nil || string(encoded) != `{"content":null,"role":"assistant","tool_calls":[{"arguments":{"id":1},"name":"lookup","tool_id":"call-1"}]}` {
		t.Fatalf("encoded tool-call message %s, err %v", encoded, err)
	}
	cached, err := json.Marshal(prompt.Template())
	if err != nil {
		t.Fatal(err)
	}
	var restored PromptTemplate
	if err := json.Unmarshal(cached, &restored); err != nil || !reflect.DeepEqual(restored, prompt.Template()) {
		t.Fatalf("restored template %#v, err %v", restored, err)
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
	if !reflect.DeepEqual(prompt.Template().Messages, []PromptMessage{{Role: "user", Content: "hello"}}) {
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
