// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package trace

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
)

type (
	// SpanOperation is a dyngo.Operation that holds a ddtrace.Span.
	// It used as a middleware for appsec code and the tracer code
	// hopefully some day this operation will create spans instead of simply holding them
	SpanOperation struct {
		dyngo.Operation
	}

	// SpanArgs is the arguments for a SpanOperation
	SpanArgs struct{}

	// SpanRes is the result for a SpanOperation
	SpanRes struct {
		ddtrace.Span
	}

	// ServiceEntrySpanOperation is a dyngo.Operation that holds a the first span of a service. Usually a http or grpc span.
	ServiceEntrySpanOperation struct {
		SpanOperation
	}

	// ServiceEntrySpanArgs is the arguments for a ServiceEntrySpanOperation
	ServiceEntrySpanArgs struct{}

	// ServiceEntrySpanRes is the result for a ServiceEntrySpanOperation
	ServiceEntrySpanRes struct {
		ddtrace.Span
	}

	// SpanTag is a key value pair that is used to tag a span
	SpanTag struct {
		Key   string
		Value any
	}

	// ServiceEntrySpanTag is a key value pair that is used to tag a service entry span
	ServiceEntrySpanTag struct {
		Key   string
		Value any
	}

	// SerializableServiceEntrySpanTag is a key value pair that is used to tag a service entry span
	// It will be serialized as JSON when added to the span
	SerializableServiceEntrySpanTag struct {
		Key   string
		Value any
	}
)

func (SpanArgs) IsArgOf(*SpanOperation)   {}
func (SpanRes) IsResultOf(*SpanOperation) {}

func (ServiceEntrySpanArgs) IsArgOf(*ServiceEntrySpanOperation)   {}
func (ServiceEntrySpanRes) IsResultOf(*ServiceEntrySpanOperation) {}

func (op *ServiceEntrySpanOperation) SetTag(key string, value any) {
	dyngo.EmitData(op, ServiceEntrySpanTag{Key: key, Value: value})
}

func (op *SpanOperation) SetTag(key string, value any) {
	dyngo.EmitData(op, SpanTag{Key: key, Value: value})
}
