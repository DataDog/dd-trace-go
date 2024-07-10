// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package slog provides functions to correlate logs and traces using log/slog package (https://pkg.go.dev/log/slog).
package slog // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/log/slog"

import (
	"context"
	"io"
	"log/slog"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "log/slog"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("log/slog")
}

// NewJSONHandler is a convenience function that returns a *slog.JSONHandler logger enhanced with
// tracing information.
func NewJSONHandler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	return WrapHandler(slog.NewJSONHandler(w, opts))
}

// WrapHandler enhances the given logger handler attaching tracing information to logs.
func WrapHandler(h slog.Handler) slog.Handler {
	return &handler{h}
}

type handler struct {
	slog.Handler
}

// Handle handles the given Record, attaching tracing information if found.
func (h *handler) Handle(ctx context.Context, rec slog.Record) error {
	span, ok := tracer.SpanFromContext(ctx)
	if ok {
		rec.Add(
			slog.Uint64(ext.LogKeyTraceID, span.Context().TraceID()),
			slog.Uint64(ext.LogKeySpanID, span.Context().SpanID()),
		)
	}
	return h.Handler.Handle(ctx, rec)
}
