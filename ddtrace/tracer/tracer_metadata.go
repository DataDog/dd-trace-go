// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.
package tracer

import (
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/DataDog/dd-trace-go/v2/internal/otelprocesscontext"
)

// Metadata represents the configuration of the tracer.
//
// NOTE: If you modify this struct, do not forget to update the toProcessContext method accordingly.
//
//go:generate go run github.com/tinylib/msgp -unexported -marshal=true -o=tracer_metadata_msgp.go -tests=false
type Metadata struct {
	// Version of the schema.
	SchemaVersion uint8 `msg:"schema_version"`
	// Runtime UUID.
	RuntimeID string `msg:"runtime_id"`
	// Programming language of the tracer.
	Language string `msg:"tracer_language"`
	// Version of the tracer
	Version string `msg:"tracer_version"`
	// Identfier of the machine running the process.
	Hostname string `msg:"hostname"`
	// Name of the service being instrumented.
	ServiceName string `msg:"service_name"`
	// Environment of the service being instrumented.
	ServiceEnvironment string `msg:"service_env"`
	// Version of the service being instrumented.
	ServiceVersion string `msg:"service_version"`
	// ProcessTags describe the process
	ProcessTags string `msg:"process_tags"`
	// ContainerID identified by the process.
	ContainerID string `msg:"container_id"`
}

// toProcessContext builds a *otelprocesscontext.ProcessContext from the Metadata fields,
// making Metadata the single source of truth for both the msgpack memfd and the proto mmap.
func (m Metadata) toProcessContext() *otelprocesscontext.ProcessContext {
	attrs := []struct{ key, val string }{
		{"deployment.environment.name", m.ServiceEnvironment},
		{"host.name", m.Hostname},
		{"service.instance.id", m.RuntimeID},
		{"service.name", m.ServiceName},
		{"service.version", m.ServiceVersion},
		{"telemetry.sdk.language", m.Language},
		{"telemetry.sdk.version", m.Version},
		{"telemetry.sdk.name", "dd-trace-go"},
		{"container.id", m.ContainerID},
	}
	kvs := make([]*commonv1.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		if a.val == "" {
			continue
		}
		kvs = append(kvs, &commonv1.KeyValue{
			Key: a.key,
			Value: &commonv1.AnyValue{
				Value: &commonv1.AnyValue_StringValue{StringValue: a.val},
			},
		})
	}
	extraAttrs := []*commonv1.KeyValue{
		{
			Key: "datadog.process_tags",
			Value: &commonv1.AnyValue{
				Value: &commonv1.AnyValue_StringValue{StringValue: m.ProcessTags},
			},
		},
	}
	return &otelprocesscontext.ProcessContext{
		Resource:        &resourcev1.Resource{Attributes: kvs},
		ExtraAttributes: extraAttrs,
	}
}
