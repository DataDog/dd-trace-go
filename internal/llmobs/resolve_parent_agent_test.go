// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import "testing"

func TestResolveParentAgent(t *testing.T) {
	t.Run("no-parent", func(t *testing.T) {
		name, id := resolveParentAgent(nil, nil)
		if name != "" || id != "" {
			t.Fatalf("expected empty, got name=%q id=%q", name, id)
		}
	})

	t.Run("parent-is-agent", func(t *testing.T) {
		parent := &Span{name: "my_agent", spanKind: SpanKindAgent}
		parent.apm = testAPMSpan{spanID: "111"}
		name, id := resolveParentAgent(parent, nil)
		if name != "my_agent" || id != "111" {
			t.Fatalf("expected (my_agent,111), got (%q,%q)", name, id)
		}
	})

	t.Run("parent-other-kind-inherits", func(t *testing.T) {
		parent := &Span{
			name:              "tool_x",
			spanKind:          SpanKindTool,
			parentAgentName:   "top_agent",
			parentAgentSpanID: "222",
		}
		parent.apm = testAPMSpan{spanID: "333"}
		name, id := resolveParentAgent(parent, nil)
		if name != "top_agent" || id != "222" {
			t.Fatalf("expected inherited (top_agent,222), got (%q,%q)", name, id)
		}
	})

	t.Run("propagated-parent", func(t *testing.T) {
		prop := &PropagatedLLMSpan{ParentAgentName: "remote_agent", ParentAgentSpanID: "444"}
		name, id := resolveParentAgent(nil, prop)
		if name != "remote_agent" || id != "444" {
			t.Fatalf("expected (remote_agent,444), got (%q,%q)", name, id)
		}
	})

	t.Run("parent-takes-precedence-over-propagated", func(t *testing.T) {
		parent := &Span{name: "local_agent", spanKind: SpanKindAgent}
		parent.apm = testAPMSpan{spanID: "555"}
		prop := &PropagatedLLMSpan{ParentAgentName: "remote_agent", ParentAgentSpanID: "444"}
		name, id := resolveParentAgent(parent, prop)
		if name != "local_agent" || id != "555" {
			t.Fatalf("expected local (local_agent,555), got (%q,%q)", name, id)
		}
	})
}

// testAPMSpan is a minimal APMSpan implementation returning fixed values.
// All methods are implemented to avoid nil-pointer panics when embedded-interface
// patterns would otherwise dispatch to a nil value.
type testAPMSpan struct {
	spanID  string
	traceID string
}

func (s testAPMSpan) Finish(_ FinishAPMSpanConfig)          {}
func (s testAPMSpan) AddLink(_ SpanLink)                    {}
func (s testAPMSpan) SpanID() string                        { return s.spanID }
func (s testAPMSpan) TraceID() string                       { return s.traceID }
func (s testAPMSpan) SetBaggageItem(_ string, _ string)     {}
func (s testAPMSpan) BaggageItem(_ string) string           { return "" }
