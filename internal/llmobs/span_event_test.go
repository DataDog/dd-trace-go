// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
)

func TestEnsureSpanEventMeta(t *testing.T) {
	event := &transport.LLMObsSpanEvent{}
	meta := llmobs.EnsureSpanEventMeta(event)
	meta["created"] = true
	assert.Equal(t, map[string]any{"created": true}, event.Meta)

	existing := map[string]any{"existing": true}
	event.Meta = existing
	meta = llmobs.EnsureSpanEventMeta(event)
	meta["added"] = true
	assert.Equal(t, map[string]any{"existing": true, "added": true}, event.Meta)
}

func TestSpanEventKind(t *testing.T) {
	assert.Equal(t, llmobs.SpanKindLLM, llmobs.SpanEventKind(&transport.LLMObsSpanEvent{
		Meta: map[string]any{"span.kind": "llm"},
	}))
	assert.Equal(t, llmobs.SpanKindLLM, llmobs.SpanEventKind(&transport.LLMObsSpanEvent{
		Meta: map[string]any{"span.kind": transport.SpanKindLLM},
	}))
	assert.Empty(t, llmobs.SpanEventKind(&transport.LLMObsSpanEvent{}))
	assert.Empty(t, llmobs.SpanEventKind(&transport.LLMObsSpanEvent{
		Meta: map[string]any{"span.kind": 1},
	}))
}

func TestApplySpanEventDefaults(t *testing.T) {
	event := &transport.LLMObsSpanEvent{
		TraceID: "trace",
		SpanID:  "span",
		Meta:    llmobs.NewSpanEventMeta(llmobs.SpanKindTool),
	}
	llmobs.ApplySpanEventDefaults(event)
	assert.Equal(t, llmobs.DefaultParentID, event.ParentID)
	assert.Equal(t, "tool", event.Name)
	assert.Equal(t, transport.SpanStatusOK, event.Status)
	assert.Equal(t, "span", event.DDAttributes.SpanID)
	assert.Equal(t, "trace", event.DDAttributes.TraceID)

	preserved := &transport.LLMObsSpanEvent{
		TraceID:  "trace",
		SpanID:   "span",
		ParentID: "parent",
		Name:     "name",
		Status:   transport.SpanStatusError,
		Meta:     llmobs.NewSpanEventMeta(llmobs.SpanKindLLM),
		DDAttributes: transport.DDAttributes{
			SpanID:  "dd-span",
			TraceID: "dd-trace",
		},
	}
	llmobs.ApplySpanEventDefaults(preserved)
	assert.Equal(t, "parent", preserved.ParentID)
	assert.Equal(t, "name", preserved.Name)
	assert.Equal(t, transport.SpanStatusError, preserved.Status)
	assert.Equal(t, "dd-span", preserved.DDAttributes.SpanID)
	assert.Equal(t, "dd-trace", preserved.DDAttributes.TraceID)
}

func TestSetSpanErrorMeta(t *testing.T) {
	meta := map[string]any{"existing": true}
	llmobs.SetSpanErrorMeta(meta, nil)
	assert.Equal(t, map[string]any{"existing": true}, meta)

	llmobs.SetSpanErrorMeta(meta, &transport.ErrorMessage{
		Message: "failed",
		Type:    "provider.Error",
		Stack:   "stack",
	})
	assert.Equal(t, "failed", meta["error.message"])
	assert.Equal(t, "provider.Error", meta["error.type"])
	assert.Equal(t, "stack", meta["error.stack"])
}

func TestSetSpanModelMetaDefaults(t *testing.T) {
	meta := map[string]any{}
	llmobs.SetSpanModelMeta(meta, llmobs.SpanKindLLM, "", "OpenAI")
	assert.Equal(t, "custom", meta["model_name"])
	assert.Equal(t, "openai", meta["model_provider"])

	llmobs.SetSpanModelMeta(meta, llmobs.SpanKindEmbedding, "embedding", "")
	assert.Equal(t, "embedding", meta["model_name"])
	assert.Equal(t, "custom", meta["model_provider"])

	llmobs.SetSpanModelMeta(meta, llmobs.SpanKindWorkflow, "", "")
	assert.NotContains(t, meta, "model_name")
	assert.NotContains(t, meta, "model_provider")
}

func TestDropSpanEventIO(t *testing.T) {
	assert.False(t, llmobs.DropSpanEventIO(nil))
	assert.False(t, llmobs.DropSpanEventIO(&transport.LLMObsSpanEvent{}))

	event := &transport.LLMObsSpanEvent{
		Meta: map[string]any{
			"input":  map[string]any{"value": "in"},
			"output": map[string]any{"value": "out"},
		},
		CollectionErrors: []string{"existing"},
	}
	assert.True(t, llmobs.DropSpanEventIO(event))
	assert.Equal(t, []string{"existing", "dropped_io"}, event.CollectionErrors)
	assert.NotEqual(t, map[string]any{"value": "in"}, event.Meta["input"])
	assert.NotEqual(t, map[string]any{"value": "out"}, event.Meta["output"])

	assert.True(t, llmobs.DropSpanEventIO(event))
	assert.Equal(t, []string{"existing", "dropped_io"}, event.CollectionErrors)
}
