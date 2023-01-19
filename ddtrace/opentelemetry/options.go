// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package opentelemetry

import (
	oteltrace "go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var locOpts []tracer.StartOption

// WithOpts is used to pass Datadog options to Otel interface.
// Might be reconfigured or removed later. Kept for development purposes.
func WithOpts(opts ...tracer.StartOption) (_ oteltrace.TracerOption) {
	locOpts = opts
	return
}
