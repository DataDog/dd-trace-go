// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"context"
	"strings"
	"testing"
)

// tagValue returns the value of key within a comma-delimited x-datadog-tags string.
func tagValue(header, key string) (string, bool) {
	for _, pair := range strings.Split(header, ",") {
		k, v, found := strings.Cut(pair, "=")
		if found && k == key {
			return v, true
		}
	}
	return "", false
}

func TestInjectAgentAttribution(t *testing.T) {
	t.Run("inject-from-agent", func(t *testing.T) {
		agent := &Span{name: "my_agent", spanKind: SpanKindAgent}
		agent.apm = testAPMSpan{spanID: "1000"}
		carrier := map[string]string{}
		injectAgentAttribution(agent, carrier)

		tags := carrier["x-datadog-tags"]
		id, ok := tagValue(tags, "_dd.p.llmobs_pagent_span_id")
		if !ok || id != "1000" {
			t.Fatalf("expected span id 1000, got %q (ok=%v) in %q", id, ok, tags)
		}
		name, ok := tagValue(tags, "_dd.p.llmobs_pagent_name")
		if !ok || name != "my_agent" {
			t.Fatalf("expected name my_agent, got %q (ok=%v)", name, ok)
		}
	})

	t.Run("inject-from-tool-under-agent-inherits", func(t *testing.T) {
		tool := &Span{name: "tool", spanKind: SpanKindTool, parentAgentName: "my_agent", parentAgentSpanID: "1000"}
		tool.apm = testAPMSpan{spanID: "2000"}
		carrier := map[string]string{}
		injectAgentAttribution(tool, carrier)

		tags := carrier["x-datadog-tags"]
		id, _ := tagValue(tags, "_dd.p.llmobs_pagent_span_id")
		name, _ := tagValue(tags, "_dd.p.llmobs_pagent_name")
		if id != "1000" || name != "my_agent" {
			t.Fatalf("tool should inject inherited agent (my_agent,1000), got (%q,%q)", name, id)
		}
	})

	t.Run("no-agent-in-chain-writes-nothing", func(t *testing.T) {
		tool := &Span{name: "tool", spanKind: SpanKindTool}
		tool.apm = testAPMSpan{spanID: "2000"}
		carrier := map[string]string{}
		injectAgentAttribution(tool, carrier)

		if _, ok := carrier["x-datadog-tags"]; ok {
			if _, hasID := tagValue(carrier["x-datadog-tags"], "_dd.p.llmobs_pagent_span_id"); hasID {
				t.Fatalf("no agent ancestor: span_id tag must not be written, got %q", carrier["x-datadog-tags"])
			}
		}
	})

	t.Run("unsafe-name-comma-writes-id-only", func(t *testing.T) {
		agent := &Span{name: "bad,name", spanKind: SpanKindAgent}
		agent.apm = testAPMSpan{spanID: "1000"}
		carrier := map[string]string{}
		injectAgentAttribution(agent, carrier)

		tags := carrier["x-datadog-tags"]
		if _, ok := tagValue(tags, "_dd.p.llmobs_pagent_span_id"); !ok {
			t.Fatalf("expected span id written, got %q", tags)
		}
		if _, ok := tagValue(tags, "_dd.p.llmobs_pagent_name"); ok {
			t.Fatalf("unsafe name must be omitted, got %q", tags)
		}
	})

	t.Run("name-with-equals-propagates", func(t *testing.T) {
		agent := &Span{name: "k=v", spanKind: SpanKindAgent}
		agent.apm = testAPMSpan{spanID: "1000"}
		carrier := map[string]string{}
		injectAgentAttribution(agent, carrier)

		name, ok := tagValue(carrier["x-datadog-tags"], "_dd.p.llmobs_pagent_name")
		if !ok || name != "k=v" {
			t.Fatalf("name with '=' must propagate, got %q (ok=%v)", name, ok)
		}
	})

	t.Run("oversized-name-writes-id-only", func(t *testing.T) {
		agent := &Span{name: strings.Repeat("a", 257), spanKind: SpanKindAgent}
		agent.apm = testAPMSpan{spanID: "1000"}
		carrier := map[string]string{}
		injectAgentAttribution(agent, carrier)

		if _, ok := tagValue(carrier["x-datadog-tags"], "_dd.p.llmobs_pagent_span_id"); !ok {
			t.Fatalf("expected span id written for oversized name")
		}
		if _, ok := tagValue(carrier["x-datadog-tags"], "_dd.p.llmobs_pagent_name"); ok {
			t.Fatalf("oversized name must be omitted")
		}
	})

	t.Run("merges-with-existing-tags", func(t *testing.T) {
		agent := &Span{name: "my_agent", spanKind: SpanKindAgent}
		agent.apm = testAPMSpan{spanID: "1000"}
		carrier := map[string]string{"x-datadog-tags": "_dd.p.dm=-0"}
		injectAgentAttribution(agent, carrier)

		tags := carrier["x-datadog-tags"]
		if _, ok := tagValue(tags, "_dd.p.dm"); !ok {
			t.Fatalf("existing tag _dd.p.dm must be preserved, got %q", tags)
		}
		if _, ok := tagValue(tags, "_dd.p.llmobs_pagent_span_id"); !ok {
			t.Fatalf("new tag must be appended, got %q", tags)
		}
	})
}

func TestExtractAgentAttribution(t *testing.T) {
	t.Run("extract-both", func(t *testing.T) {
		carrier := map[string]string{
			"x-datadog-tags": "_dd.p.llmobs_pagent_span_id=1000,_dd.p.llmobs_pagent_name=my_agent",
		}
		name, id, present := extractAgentAttribution(carrier)
		if !present || id != "1000" || name != "my_agent" {
			t.Fatalf("expected (my_agent,1000,true), got (%q,%q,%v)", name, id, present)
		}
	})

	t.Run("extract-id-only", func(t *testing.T) {
		carrier := map[string]string{
			"x-datadog-tags": "_dd.p.llmobs_pagent_span_id=1000",
		}
		name, id, present := extractAgentAttribution(carrier)
		if !present || id != "1000" || name != "" {
			t.Fatalf("expected id-only (\"\",1000,true), got (%q,%q,%v)", name, id, present)
		}
	})

	t.Run("extract-absent", func(t *testing.T) {
		carrier := map[string]string{"x-datadog-tags": "_dd.p.dm=-0"}
		_, _, present := extractAgentAttribution(carrier)
		if present {
			t.Fatalf("expected present=false when no llmobs tags")
		}
	})
}

func TestExtractContextUpdatesExistingPropagated(t *testing.T) {
	// A prior ExtractContext (or manual set) put ML app + trace id on the context.
	base := ContextWithPropagatedLLMSpan(context.Background(), &PropagatedLLMSpan{
		MLApp:   "existing_app",
		TraceID: "trace123",
		SpanID:  "span456",
	})
	carrier := map[string]string{
		"x-datadog-tags": "_dd.p.llmobs_pagent_span_id=1000,_dd.p.llmobs_pagent_name=my_agent",
	}
	out := ExtractContext(base, carrier)
	prop, ok := PropagatedLLMSpanFromContext(out)
	if !ok {
		t.Fatal("expected a propagated span on the returned context")
	}
	if prop.MLApp != "existing_app" || prop.TraceID != "trace123" || prop.SpanID != "span456" {
		t.Fatalf("existing propagated fields must be preserved, got %+v", prop)
	}
	if prop.ParentAgentName != "my_agent" || prop.ParentAgentSpanID != "1000" {
		t.Fatalf("agent fields must be set, got name=%q id=%q", prop.ParentAgentName, prop.ParentAgentSpanID)
	}
}

func TestInjectContextReadsActiveSpan(t *testing.T) {
	agent := &Span{name: "my_agent", spanKind: SpanKindAgent}
	agent.apm = testAPMSpan{spanID: "1000"}
	ctx := contextWithActiveLLMSpan(context.Background(), agent)
	carrier := map[string]string{}
	InjectContext(ctx, carrier)

	name, _ := tagValue(carrier["x-datadog-tags"], "_dd.p.llmobs_pagent_name")
	id, _ := tagValue(carrier["x-datadog-tags"], "_dd.p.llmobs_pagent_span_id")
	if name != "my_agent" || id != "1000" {
		t.Fatalf("InjectContext should inject active agent (my_agent,1000), got (%q,%q)", name, id)
	}
}
