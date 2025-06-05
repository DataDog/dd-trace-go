// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"context"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// ContextWithStartOptions returns a copy of the given context which includes the span s.
// This can be used to pass a context with Datadog start options to the Start function on the OTel tracer to propagate the options.
func ContextWithStartOptions(ctx context.Context, opts ...tracer.StartSpanOption) context.Context {
	return v2.ContextWithStartOptions(ctx, internal.ApplyV1Options(opts...))
}
