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
	"strconv"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const componentName = "log/slog"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("log/slog")
}

var _ slog.Handler = (*handler)(nil)

type group struct {
	name  string
	attrs []slog.Attr
}

// NewJSONHandler is a convenience function that returns a *slog.JSONHandler logger enhanced with
// tracing information.
func NewJSONHandler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	return WrapHandler(slog.NewJSONHandler(w, opts))
}

// WrapHandler enhances the given logger handler attaching tracing information to logs.
func WrapHandler(h slog.Handler) slog.Handler {
	return &handler{wrapped: h}
}

type handler struct {
	wrapped slog.Handler
	groups  []group
}

// Enabled calls the wrapped handler Enabled method.
func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.wrapped.Enabled(ctx, level)
}

// Handle handles the given Record, attaching tracing information if found.
func (h *handler) Handle(ctx context.Context, rec slog.Record) error {
	reqHandler := h.wrapped

	// We need to ensure the trace id and span id keys are set at the root level:
	// https://docs.datadoghq.com/tracing/other_telemetry/connect_logs_and_traces/
	// In case the user has created group loggers, we ignore those and
	// set them at the root level.
	span, ok := tracer.SpanFromContext(ctx)
	if ok {
		traceID := strconv.FormatUint(span.Context().TraceID(), 10)
		spanID := strconv.FormatUint(span.Context().SpanID(), 10)

		attrs := []slog.Attr{
			slog.String(ext.LogKeyTraceID, traceID),
			slog.String(ext.LogKeySpanID, spanID),
		}
		reqHandler = reqHandler.WithAttrs(attrs)
	}
	for _, g := range h.groups {
		reqHandler = reqHandler.WithGroup(g.name)
		if len(g.attrs) > 0 {
			reqHandler = reqHandler.WithAttrs(g.attrs)
		}
	}
	return reqHandler.Handle(ctx, rec)
}

// WithAttrs saves the provided attributes associated to the current Group.
// If Group was not called for the logger, we just call WithAttrs for the wrapped handler.
func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(h.groups) == 0 {
		return &handler{
			wrapped: h.wrapped.WithAttrs(attrs),
			groups:  h.groups,
		}
	}
	groups := append([]group{}, h.groups...)
	curGroup := groups[len(groups)-1]
	curGroup.attrs = append(curGroup.attrs, attrs...)
	groups[len(groups)-1] = curGroup

	return &handler{
		wrapped: h.wrapped,
		groups:  groups,
	}
}

// WithGroup saves the provided group to be used later in the Handle method.
func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{
		wrapped: h.wrapped,
		groups:  append(h.groups, group{name: name}),
	}
}
