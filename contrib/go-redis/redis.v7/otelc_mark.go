// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package redis

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// otelcEnabled is a variable so tests can turn the otelc behavior on.
var otelcEnabled = instrumentation.OtelcEnabled

type traceMarkKey struct{}

// markOrSkip, in an otelc build, reports whether another hook of this package
// already traces the command in ctx. If not, it returns ctx marked with h.
//
// The otelc rules wrap a Ring or Cluster and also the clients that its
// NewClient option builds with redis.NewClient, so both hooks see the same
// command. The mark keeps the outer span only. Plain builds skip all of it.
func markOrSkip(ctx context.Context, h *datadogHook) (context.Context, bool) {
	if !otelcEnabled() {
		return ctx, false
	}
	if tracedByOther(ctx, h) {
		return ctx, true
	}
	return context.WithValue(ctx, traceMarkKey{}, h), false
}

// tracedByOther reports whether ctx was marked by a hook other than h.
func tracedByOther(ctx context.Context, h *datadogHook) bool {
	if !otelcEnabled() {
		return false
	}
	marked, _ := ctx.Value(traceMarkKey{}).(*datadogHook)
	return marked != nil && marked != h
}
