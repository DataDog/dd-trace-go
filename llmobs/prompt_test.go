// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"reflect"
	"testing"
)

func TestPromptTextAndChat(t *testing.T) {
	text, err := parsePrompt([]byte(`{"prompt_id":"greeting","user_version":"v1","template":"Hello {name}; {{ missing }}","prompt_uuid":"p","prompt_version_uuid":"v"}`), PromptSourceRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if got := text.Format(map[string]any{"name": 42}).Text; got != "Hello 42; {{ missing }}" {
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
	rendered := chat.Format(map[string]any{"name": "Ada"})
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
