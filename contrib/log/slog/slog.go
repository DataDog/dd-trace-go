// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package slog provides functions to correlate logs and traces using log/slog package (https://pkg.go.dev/log/slog).
package slog // import "github.com/DataDog/dd-trace-go/contrib/log/slog/v2"

import (
	"context"
	"io"
	"log/slog"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageLogSlog)
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
			slog.String(ext.LogKeyTraceID, span.Context().TraceID()),
			slog.Uint64(ext.LogKeySpanID, span.Context().SpanID()),
		)
	}
	return h.Handler.Handle(ctx, rec)
}
