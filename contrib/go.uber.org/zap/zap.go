// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

// Package ddzap provides Datadog trace/log correlation helpers for go.uber.org/zap.
//
// # Manual use
//
// Call TraceFields with the current context to obtain zap.Field values for the
// active span, then pass them to any Logger method:
//
//	logger.Info("handling request",
//	    zap.String("url", req.URL.Path),
//	    ddzap.TraceFields(ctx)...,
//	)
//
// # Automatic use via Orchestrion
//
// When built with Orchestrion, call sites of (*zap.Logger) and (*zap.SugaredLogger)
// log methods are automatically rewritten to inject trace fields whenever a
// context.Context (or *http.Request) is in scope.
package ddzap

import (
	"context"
	"strconv"

	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// TraceFields returns zap.Field values carrying the Datadog trace and span IDs
// from the active span in ctx. Returns nil when no span is active.
func TraceFields(ctx context.Context) []zap.Field {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return nil
	}
	return []zap.Field{
		zap.String(ext.LogKeyTraceID, span.Context().TraceID()),
		zap.String(ext.LogKeySpanID, strconv.FormatUint(span.Context().SpanID(), 10)),
	}
}
