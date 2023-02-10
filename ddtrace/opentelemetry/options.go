// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"go.opentelemetry.io/otel/attribute"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

// ServiceName can be used with opentelemetry.Start to set the
// service name of a span.
func ServiceName(name string) attribute.KeyValue {
	return attribute.String(ext.ServiceName, name)
}

// ResourceName can be used with opentelemetry.Start to set the
// resource name of a span.
func ResourceName(name string) attribute.KeyValue {
	return attribute.String(ext.ResourceName, name)
}
