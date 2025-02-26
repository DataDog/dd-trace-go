// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package protobuf

import (
	"context"
	"google.golang.org/protobuf/proto"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "google.golang.org/protobuf"

type operation string

const (
	deserializeOperation operation = "deserialization"
	serializeOperation   operation = "serialization"
)

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported(componentName)
}

func attachSchemaOnSpan(ctx context.Context, m proto.Message, operation operation) {
	shouldSample := datastreams.ShouldSampleSchema()
	if !shouldSample {
		return
	}
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	if p, ok := span.Context().SamplingPriority(); !ok || p < 1 {
		return
	}
	// todo: How can I check if the span is a P1?
	if weight := datastreams.SampleSchema(); weight > 0 {
		schema, name, err := getSchema(m)
		if err == nil {
			span.SetTag(schemaDefinition, schema)
			span.SetTag(schemaID, datastreams.GetSchemaID(schema))
			span.SetTag(schemaWeight, weight)
			span.SetTag(schemaType, "protobuf")
			span.SetTag(schemaOperation, operation)
			span.SetTag(schemaName, name)
		}
	}
}

// Unmarshal un-marshals a proto message and captures the schema used if a span is present in the context
func Unmarshal(ctx context.Context, b []byte, m proto.Message) error {
	attachSchemaOnSpan(ctx, m, deserializeOperation)
	return proto.Unmarshal(b, m)
}

// Marshal marshals a proto message and captures the schema used if a span is present in the context
func Marshal(ctx context.Context, m proto.Message) (data []byte, err error) {
	attachSchemaOnSpan(ctx, m, serializeOperation)
	return proto.Marshal(m)
}
