// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otelprocesscontext

//go:generate ./proto/generate.sh

import (
	"google.golang.org/protobuf/proto"
)

// OtelProcessContext holds the OTel resource attributes for this process.
type OtelProcessContext struct {
	// https://opentelemetry.io/docs/specs/semconv/registry/attributes/deployment/#deployment-environment-name
	DeploymentEnvironmentName string
	// https://opentelemetry.io/docs/specs/semconv/registry/attributes/host/#host-name
	HostName string
	// https://opentelemetry.io/docs/specs/semconv/registry/attributes/service/#service-instance-id
	ServiceInstanceID string
	// https://opentelemetry.io/docs/specs/semconv/registry/attributes/service/#service-name
	ServiceName string
	// https://opentelemetry.io/docs/specs/semconv/registry/attributes/service/#service-version
	ServiceVersion string
	// https://opentelemetry.io/docs/specs/semconv/registry/attributes/telemetry/#telemetry-sdk-language
	TelemetrySDKLanguage string
	// https://opentelemetry.io/docs/specs/semconv/registry/attributes/telemetry/#telemetry-sdk-version
	TelemetrySDKVersion string
	// https://opentelemetry.io/docs/specs/semconv/registry/attributes/telemetry/#telemetry-sdk-name
	TelemetrySdkName string
}

// MarshalProto encodes ctx as a protobuf ProcessContext message
func (ctx OtelProcessContext) marshalProto() []byte {
	attrs := []struct{ key, val string }{
		{"deployment.environment.name", ctx.DeploymentEnvironmentName},
		{"host.name", ctx.HostName},
		{"service.instance.id", ctx.ServiceInstanceID},
		{"service.name", ctx.ServiceName},
		{"service.version", ctx.ServiceVersion},
		{"telemetry.sdk.language", ctx.TelemetrySDKLanguage},
		{"telemetry.sdk.version", ctx.TelemetrySDKVersion},
		{"telemetry.sdk.name", ctx.TelemetrySdkName},
	}

	kvs := make([]*KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		if attr.val == "" {
			continue
		}
		kvs = append(kvs, &KeyValue{
			Key:   attr.key,
			Value: &AnyValue{Value: &AnyValue_StringValue{StringValue: attr.val}},
		})
	}

	b, _ := proto.Marshal(&ProcessContext{
		Resource: &Resource{Attributes: kvs},
	})
	return b
}

// Publish publishes the OtelProcessContext to the memory-mapped region
func (ctx OtelProcessContext) Publish() error {
	data := ctx.marshalProto()
	return CreateOtelProcessContextMapping(data)
}
