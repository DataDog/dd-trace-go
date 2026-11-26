// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package kgo

import (
	"reflect"

	kgo "github.com/twmb/franz-go/pkg/kgo"
)

// shadowed reports whether c has another tracing hook before h. A client gets
// two when the application passes WithTracing twice, or passes it and
// compile-time instrumentation adds it again. Each hook starts a span for a
// record, but only one of the spans is finished, so the trace is never sent.
// The first hook keeps tracing.
func shadowed(c optValuer, h *tracingHook) bool {
	// The hooks have an unexported slice type.
	hooks := reflect.ValueOf(c.OptValue(kgo.WithHooks))
	if hooks.Kind() != reflect.Slice {
		return false
	}
	for i := range hooks.Len() {
		if first, ok := reflect.TypeAssert[*tracingHook](hooks.Index(i)); ok {
			return first != h
		}
	}
	return false
}
