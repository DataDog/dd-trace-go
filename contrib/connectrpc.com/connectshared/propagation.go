// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connectshared

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// propagationOptions returns the start options that make a span a child of the span context
// propagated in carrier, with its span links, or nil if carrier propagates none. The error is the
// tracer's failure to extract one: a tracer that doesn't extract, such as the one used while
// tracing is disabled, returns no error.
func propagationOptions(carrier tracer.TextMapReader) ([]tracer.StartSpanOption, error) {
	spanContext, err := tracer.Extract(carrier)
	if err != nil || spanContext == nil {
		return nil, err
	}
	opts := []tracer.StartSpanOption{tracer.ChildOf(spanContext)}
	if links := spanContext.SpanLinks(); links != nil {
		opts = append(opts, tracer.WithSpanLinks(links))
	}
	return opts, nil
}

// Inject injects the span of ctx, if any, into carrier. A failure is logged as a warning.
func (c *Config) Inject(ctx context.Context, carrier tracer.TextMapWriter) {
	inject(ctx, carrier, c.instr.Logger(), c.logPrefix)
}

// inject injects the span of ctx, if any, into carrier. A failure is logged as a warning that
// starts with logPrefix.
func inject(ctx context.Context, carrier tracer.TextMapWriter, logger instrumentation.Logger, logPrefix string) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	if err := tracer.Inject(span.Context(), carrier); err != nil {
		logger.Warn("%s: failed to inject trace context: %s", logPrefix, err.Error())
	}
}
