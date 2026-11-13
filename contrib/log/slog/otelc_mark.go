// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package slog

import (
	"context"
	"log/slog"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// otelcEnabled is a variable so tests can turn the otelc behavior on.
var otelcEnabled = instrumentation.OtelcEnabled

type correlatedKey struct{}

// markCorrelated, in an otelc build, returns ctx marked to show that a handler
// of this package added the trace IDs to the record.
//
// The otelc rules wrap the handler of every slog.New call, so a handler that
// holds one of ours, such as a third-party handler built around
// logger.Handler(), gets a second one around it. The outer one passes its IDs
// down with WithAttrs, and the inner one skips its own only when they reached
// its root level (outerIDs), not a group it already had open. Plain builds
// skip all of it.
func markCorrelated(ctx context.Context) context.Context {
	if !otelcEnabled() {
		return ctx
	}
	return context.WithValue(ctx, correlatedKey{}, struct{}{})
}

// correlatedByOuter reports whether an outer handler of this package already
// put the trace IDs of the record at the root level of h.
func (h *handler) correlatedByOuter(ctx context.Context) bool {
	return otelcEnabled() && h.outerIDs && ctx.Value(correlatedKey{}) != nil
}

// hasTraceID reports, in an otelc build, whether attrs hold a trace ID.
func hasTraceID(attrs []slog.Attr) bool {
	if !otelcEnabled() {
		return false
	}
	for _, a := range attrs {
		if a.Key == ext.LogKeyTraceID {
			return true
		}
	}
	return false
}
