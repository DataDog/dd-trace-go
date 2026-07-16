// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"context"
	"fmt"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
)

// resolveTestTracer is a minimal Tracer that returns testAPMSpans with unique IDs.
type resolveTestTracer struct {
	next int
}

func (tr *resolveTestTracer) StartSpan(ctx context.Context, _ string, _ StartAPMSpanConfig) (APMSpan, context.Context) {
	tr.next++
	return testAPMSpan{spanID: fmt.Sprintf("id-%d", tr.next)}, ctx
}

func newTestLLMObsForResolve(t *testing.T) *LLMObs {
	t.Helper()
	return &LLMObs{
		Config: &config.Config{Enabled: true, MLApp: "gotest"},
		Tracer: &resolveTestTracer{},
	}
}

func TestStartSpanResolvesParentAgent(t *testing.T) {
	l := newTestLLMObsForResolve(t)
	ctx := context.Background()

	t.Run("tool-under-agent", func(t *testing.T) {
		agent, agentCtx := l.StartSpan(ctx, SpanKindAgent, "my_agent", StartSpanConfig{})
		tool, _ := l.StartSpan(agentCtx, SpanKindTool, "my_tool", StartSpanConfig{})
		if tool.parentAgentName != "my_agent" {
			t.Fatalf("tool.parentAgentName = %q, want my_agent", tool.parentAgentName)
		}
		if tool.parentAgentSpanID != agent.SpanID() {
			t.Fatalf("tool.parentAgentSpanID = %q, want %q", tool.parentAgentSpanID, agent.SpanID())
		}
	})

	t.Run("agent-workflow-tool-indirect-nesting", func(t *testing.T) {
		agent, agentCtx := l.StartSpan(ctx, SpanKindAgent, "my_agent", StartSpanConfig{})
		wf, wfCtx := l.StartSpan(agentCtx, SpanKindWorkflow, "wf", StartSpanConfig{})
		tool, _ := l.StartSpan(wfCtx, SpanKindTool, "tool", StartSpanConfig{})
		if wf.parentAgentSpanID != agent.SpanID() || wf.parentAgentName != "my_agent" {
			t.Fatalf("workflow should attribute to top agent, got (%q,%q)", wf.parentAgentName, wf.parentAgentSpanID)
		}
		if tool.parentAgentSpanID != agent.SpanID() || tool.parentAgentName != "my_agent" {
			t.Fatalf("tool should attribute to top agent, got (%q,%q)", tool.parentAgentName, tool.parentAgentSpanID)
		}
	})

	t.Run("sub-agent-under-agent", func(t *testing.T) {
		outer, outerCtx := l.StartSpan(ctx, SpanKindAgent, "outer_agent", StartSpanConfig{})
		inner, innerCtx := l.StartSpan(outerCtx, SpanKindAgent, "inner_agent", StartSpanConfig{})
		innerTool, _ := l.StartSpan(innerCtx, SpanKindTool, "inner_tool", StartSpanConfig{})
		if inner.parentAgentSpanID != outer.SpanID() || inner.parentAgentName != "outer_agent" {
			t.Fatalf("inner agent should attribute to outer, got (%q,%q)", inner.parentAgentName, inner.parentAgentSpanID)
		}
		if innerTool.parentAgentSpanID != inner.SpanID() || innerTool.parentAgentName != "inner_agent" {
			t.Fatalf("inner tool should attribute to inner agent, got (%q,%q)", innerTool.parentAgentName, innerTool.parentAgentSpanID)
		}
	})

	t.Run("top-level-agent-has-no-attribution", func(t *testing.T) {
		agent, _ := l.StartSpan(ctx, SpanKindAgent, "top_agent", StartSpanConfig{})
		if agent.parentAgentName != "" || agent.parentAgentSpanID != "" {
			t.Fatalf("top-level agent must have empty attribution, got (%q,%q)", agent.parentAgentName, agent.parentAgentSpanID)
		}
	})

	t.Run("top-level-llm-has-no-attribution", func(t *testing.T) {
		llm, _ := l.StartSpan(ctx, SpanKindLLM, "top_llm", StartSpanConfig{})
		if llm.parentAgentName != "" || llm.parentAgentSpanID != "" {
			t.Fatalf("top-level llm must have empty attribution, got (%q,%q)", llm.parentAgentName, llm.parentAgentSpanID)
		}
	})

	t.Run("workflow-then-tool-no-agent-anywhere", func(t *testing.T) {
		_, wfCtx := l.StartSpan(ctx, SpanKindWorkflow, "wf", StartSpanConfig{})
		tool, _ := l.StartSpan(wfCtx, SpanKindTool, "tool", StartSpanConfig{})
		if tool.parentAgentName != "" || tool.parentAgentSpanID != "" {
			t.Fatalf("tool with no agent ancestor must have empty attribution, got (%q,%q)", tool.parentAgentName, tool.parentAgentSpanID)
		}
	})
}
