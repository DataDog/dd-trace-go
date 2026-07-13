// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

// Package zap provides Datadog trace/log correlation helpers for go.uber.org/zap.
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
// context.Context (or a value implementing it) or a *http.Request is in scope.
package zap

import (
	"context"
	"net/http"
	"strconv"

	"go.uber.org/zap"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageGoUberOrgZap)
}

type config struct {
	log128bits bool
}

var cfg = newConfig()

func newConfig() *config {
	return &config{
		log128bits: options.GetBoolEnv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", true),
	}
}

// TraceFields returns zap.Field values carrying the Datadog trace and span IDs
// from the active span in ctx. Returns nil when no span is active.
func TraceFields(ctx context.Context) []zap.Field {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return nil
	}
	var traceID string
	if cfg.log128bits && span.Context().TraceID() != tracer.TraceIDZero {
		traceID = span.Context().TraceID()
	} else {
		traceID = strconv.FormatUint(span.Context().TraceIDLower(), 10)
	}
	return []zap.Field{
		zap.String(ext.LogKeyTraceID, traceID),
		zap.String(ext.LogKeySpanID, strconv.FormatUint(span.Context().SpanID(), 10)),
	}
}

// TraceFieldsFromRequest returns TraceFields for req's context, or nil when req
// is nil. Orchestrion uses this as a fallback trace source when a *http.Request
// is in scope but no context.Context is: a nil req would otherwise panic on
// req.Context() in an instrumented build where the original, uninstrumented log
// call would have logged normally.
func TraceFieldsFromRequest(req *http.Request) []zap.Field {
	if req == nil {
		return nil
	}
	return TraceFields(req.Context())
}
