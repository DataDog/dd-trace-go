// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package trace

import (
	"context"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
)

type (
	// SpanOperation is a dyngo.Operation that holds a ddtrace.Span.
	// It used as a middleware for appsec code and the tracer code
	// hopefully some day this operation will create spans instead of simply using them
	SpanOperation struct {
		dyngo.Operation
		Tags  map[string]any
		Mutex sync.Mutex
	}

	// SpanArgs is the arguments for a SpanOperation
	SpanArgs struct{}

	// SpanRes is the result for a SpanOperation
	SpanRes struct {
		ddtrace.Span
	}

	// SpanTag is a key value pair event that is used to tag the current span
	SpanTag struct {
		Key   string
		Value any
	}
)

func (SpanArgs) IsArgOf(*SpanOperation)   {}
func (SpanRes) IsResultOf(*SpanOperation) {}

// SetTag adds the key/value pair to the tags to add to the span
func (op *SpanOperation) SetTag(key string, value any) {
	op.Mutex.Lock()
	defer op.Mutex.Unlock()
	op.Tags[key] = value
}

// OnSpanTagEvent is a listener for SpanTag events.
func (op *SpanOperation) OnSpanTagEvent(tag SpanTag) {
	op.SetTag(tag.Key, tag.Value)
}

func (op *SpanOperation) Start(ctx context.Context) context.Context {
	return dyngo.StartAndRegisterOperation(ctx, op, SpanArgs{})
}

func (op *SpanOperation) Finish(span ddtrace.Span) {
	dyngo.FinishOperation(op, SpanRes{Span: span})
}
