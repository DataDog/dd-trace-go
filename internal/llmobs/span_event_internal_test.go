// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package llmobs

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
)

// reusedAPMSpan is an APM span that the span pool gave to another span: its ID
// is no longer the ID of the LLMObs span that holds it.
type reusedAPMSpan struct{ id string }

func (s *reusedAPMSpan) Finish(FinishAPMSpanConfig)    {}
func (s *reusedAPMSpan) AddLink(SpanLink)              {}
func (s *reusedAPMSpan) SpanID() string                { return s.id }
func (s *reusedAPMSpan) TraceID() string               { return "1" }
func (s *reusedAPMSpan) SetBaggageItem(string, string) {}
func (s *reusedAPMSpan) BaggageItem(string) string     { return "" }

func TestSpanEventUsesCachedSpanIDs(t *testing.T) {
	// The parent's APM span can be flushed and reused by the span pool before a
	// child LLMObs span finishes, so the event must use the IDs cached at start.
	l := &LLMObs{Config: &config.Config{}}
	parent := &Span{apm: &reusedAPMSpan{id: "999"}, spanID: "111", spanKind: SpanKindAgent}
	child := &Span{apm: &reusedAPMSpan{id: "998"}, spanID: "222", spanKind: SpanKindTool, parent: parent, llmTraceID: "t"}

	event := l.llmobsSpanEvent(child)
	assert.Equal(t, "222", event.SpanID)
	assert.Equal(t, "111", event.ParentID)
}
