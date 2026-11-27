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

// correlatedIDs are a trace ID and a span ID, as they appear in a record.
type correlatedIDs struct {
	traceID string
	spanID  string
}

// markCorrelated, in an otelc build, returns ctx marked with the trace IDs a
// handler of this package added to the record.
//
// The otelc rules wrap the handler of every slog.New call, so a handler that
// holds one of ours, such as a third-party handler built around
// logger.Handler(), gets a second one around it. The outer one passes its IDs
// down with WithAttrs, and the inner one skips its own only when the same IDs
// reached its root level (rootIDs), not a group it already had open. An older
// dd.trace_id set by the application does not match. Plain builds skip all of
// it.
func markCorrelated(ctx context.Context, ids correlatedIDs) context.Context {
	if !otelcEnabled() {
		return ctx
	}
	return context.WithValue(ctx, correlatedKey{}, ids)
}

// correlatedByOuter reports whether an outer handler of this package already
// put the trace IDs of the record at the root level of h.
func (h *handler) correlatedByOuter(ctx context.Context) bool {
	if !otelcEnabled() {
		return false
	}
	ids, ok := ctx.Value(correlatedKey{}).(correlatedIDs)
	return ok && h.rootIDs == ids
}

// withRootIDs returns ids with the trace IDs in attrs, in an otelc build.
func withRootIDs(ids correlatedIDs, attrs []slog.Attr) correlatedIDs {
	if !otelcEnabled() {
		return ids
	}
	for _, a := range attrs {
		switch a.Key {
		case ext.LogKeyTraceID:
			ids.traceID = a.Value.String()
		case ext.LogKeySpanID:
			ids.spanID = a.Value.String()
		}
	}
	return ids
}
