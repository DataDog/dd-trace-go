// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"sync/atomic"
)

var (
	// globalTracer stores the current tracer as *ddtrace.Tracer (pointer to interface). The
	// atomic.Value type requires types to be consistent, which requires using *ddtrace.Tracer.
	globalTracer atomic.Value
)

func init() {
	var tracer Tracer = &NoopTracer{}
	globalTracer.Store(&tracer)
}

// SetGlobalTracer sets the global tracer to t.
func SetGlobalTracer(t Tracer) {
	old := *globalTracer.Swap(&t).(*Tracer)
	old.Stop()
}

// GetGlobalTracer returns the currently active tracer.
func GetGlobalTracer() Tracer {
	return *globalTracer.Load().(*Tracer)
}

func StopTestTracer() {
	var tracer Tracer = &NoopTracer{}
	globalTracer.Swap(&tracer)
}
