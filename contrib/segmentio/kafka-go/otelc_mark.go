// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package kafka

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// otelcEnabled is a variable so tests can turn the otelc behavior on.
var otelcEnabled = instrumentation.OtelcEnabled

type traceMarkKey struct{}

// traceMark, in an otelc build, tags ctx as a call that [Reader] or
// [KafkaWriter] makes on the kafka.Reader or kafka.Writer it wraps, which it
// already traces. Plain builds return ctx unchanged.
func traceMark(ctx context.Context, target any) context.Context {
	if !otelcEnabled() {
		return ctx
	}
	return context.WithValue(ctx, traceMarkKey{}, target)
}

// TraceMarked reports whether ctx belongs to a call that this package already
// traces on target, a *kafka.Reader or *kafka.Writer. The otelc hooks use it to
// leave those calls alone.
func TraceMarked(ctx context.Context, target any) bool {
	if ctx == nil || target == nil {
		return false
	}
	return ctx.Value(traceMarkKey{}) == target
}
