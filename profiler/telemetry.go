// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

// startTelemetry starts the global instrumentation telemetry client with profiler data
// unless instrumentation telemetry is disabled via the DD_INSTRUMENTATION_TELEMETRY_ENABLED
// env var.
// If the telemetry client has already been started by the tracer, then
// app-product-change event is queued to signal the profiler is enabled, and an
// app-client-configuration-change event is also queued with profiler config data.
func startTelemetry(c *config) {
	if telemetry.Disabled() {
		// Do not do extra work populating config data if instrumentation telemetry is disabled.
		return
	}
	profileEnabled := func(t ProfileType) bool {
		_, ok := c.types[t]
		return ok
	}
	telemetry.GlobalClient.ApplyOps(
		telemetry.WithService(c.service),
		telemetry.WithEnv(c.env),
		telemetry.WithHTTPClient(c.httpClient),
		telemetry.WithURL(c.agentless, c.agentURL),
	)
	telemetry.GlobalClient.ProductChange(
		telemetry.NamespaceProfilers,
		true,
		[]telemetry.Configuration{
			{Name: "delta_profiles", Value: c.deltaProfiles},
			{Name: "agentless", Value: c.agentless},
			{Name: "profile_period", Value: c.period.String()},
			{Name: "cpu_duration", Value: c.cpuDuration.String()},
			{Name: "cpu_profile_rate", Value: c.cpuProfileRate},
			{Name: "block_profile_rate", Value: c.blockRate},
			{Name: "mutex_profile_fraction", Value: c.mutexFraction},
			{Name: "max_goroutines_wait", Value: c.maxGoroutinesWait},
			{Name: "cpu_profile_enabled", Value: profileEnabled(CPUProfile)},
			{Name: "heap_profile_enabled", Value: profileEnabled(HeapProfile)},
			{Name: "block_profile_enabled", Value: profileEnabled(BlockProfile)},
			{Name: "mutex_profile_enabled", Value: profileEnabled(MutexProfile)},
			{Name: "goroutine_profile_enabled", Value: profileEnabled(GoroutineProfile)},
			{Name: "goroutine_wait_profile_enabled", Value: profileEnabled(expGoroutineWaitProfile)},
			{Name: "upload_timeout", Value: c.uploadTimeout.String()},
			{Name: "execution_trace_enabled", Value: c.traceConfig.Enabled},
			{Name: "execution_trace_period", Value: c.traceConfig.Period.String()},
			{Name: "execution_trace_size_limit", Value: c.traceConfig.Limit},
			{Name: "endpoint_count_enabled", Value: c.endpointCountEnabled},
			{Name: "num_custom_profiler_label_keys", Value: len(c.customProfilerLabels)},
		},
	)
}
