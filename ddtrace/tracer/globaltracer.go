// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import "github.com/DataDog/dd-trace-go/v2/ddtrace/internal"

func init() {
	var tracer Tracer = &NoopTracer{}
	internal.GlobalTracer.Store(&tracer)
}

// SetGlobalTracer sets the global tracer to t.
func SetGlobalTracer(t Tracer) {
	old := *internal.GlobalTracer.Swap(&t).(*Tracer)
	old.Stop()
}

// GetGlobalTracer returns the currently active tracer.
func GetGlobalTracer() Tracer {
	return *internal.GlobalTracer.Load().(*Tracer)
}
