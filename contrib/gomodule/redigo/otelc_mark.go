// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package redigo

import "context"

type traceMarkKey struct{}

// TraceMark tags ctx as a dial this package is already tracing.
//
// The otelc rules hook redis.DialContext, which this package calls itself. The
// mark tells those two apart, so a connection dialled through this package in an
// otelc build is wrapped once rather than twice. Without it, mixing otelc with a
// direct Dial call here reports two spans per command.
func TraceMark(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, traceMarkKey{}, struct{}{})
}

// TraceMarked reports whether ctx was tagged by [TraceMark].
func TraceMarked(ctx context.Context) bool {
	return ctx != nil && ctx.Value(traceMarkKey{}) != nil
}
