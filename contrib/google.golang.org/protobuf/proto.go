// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package protobuf

import (
	"context"
	"google.golang.org/protobuf/proto"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/datastreams"
)

// Unmarshal un-marshals a proto message and captures the schema used if a span is present in the context
func Unmarshal(ctx context.Context, b []byte, m proto.Message) error {
	span, ok := tracer.SpanFromContext(ctx)
	// todo: How can I check if the span is a P1?
	if ok {
		weight := datastreams.SampleSchema()
		if weight > 0 {
			schema, name, err := getSchema(m)
			if err == nil {
				span.SetTag(schemaDefinition, schema)
				span.SetTag(schemaId, datastreams.GetSchemaID(schema))
				span.SetTag(schemaWeight, weight)
				span.SetTag(schemaType, "protobuf")
				span.SetTag(schemaOperation, "deserialization")
				span.SetTag(ext.SamplingPriority, ext.PriorityUserKeep)
				span.SetTag(schemaName, name)
			}
		}
	}
	return proto.Unmarshal(b, m)
}

// Marshal marshals a proto message and captures the schema used if a span is present in the context
func Marshal(ctx context.Context, m proto.Message) (data []byte, err error) {
	span, ok := tracer.SpanFromContext(ctx)
	// todo: How can I check if the span is a P1?
	if ok {
		weight := datastreams.SampleSchema()
		if weight > 0 {
			schema, name, err := getSchema(m)
			if err == nil {
				span.SetTag(schemaDefinition, schema)
				span.SetTag(schemaId, datastreams.GetSchemaID(schema))
				span.SetTag(schemaWeight, weight)
				span.SetTag(schemaType, "protobuf")
				span.SetTag(schemaOperation, "serialization")
				span.SetTag(ext.SamplingPriority, ext.PriorityUserKeep)
				span.SetTag(schemaName, name)
			}
		}
	}
	return proto.Marshal(m)
}
