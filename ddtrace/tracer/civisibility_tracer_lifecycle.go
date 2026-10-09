// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

// setGlobalTracerWithCIVisibility configures CI lifecycle hooks before publishing
// globalTracer, retaining an active mock router when it accepts the CI tracer.
func setGlobalTracerWithCIVisibility(globalTracer Tracer, ciVisibilityEnabled bool) {
	if ciVisibilityEnabled {
		installCIVisibilityFlushHandler(globalTracer)
		if current, ok := getGlobalTracer().(interface{ SetCIVisibilityTracer(Tracer) bool }); ok && current.SetCIVisibilityTracer(globalTracer) {
			return
		}
	}

	setGlobalTracer(globalTracer)
}
