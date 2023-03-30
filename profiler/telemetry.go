// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

// startTelemetry notifies the global telemetry client that the profiler has started
// and enqueues profiler config data to be sent to the telemetry backend.
func startTelemetry(c *config) {
	if telemetry.Disabled() {
		// Do not do extra work populating config data if instrumentation telemetry is disabled.
		return
	}
	profileEnabled := func(t ProfileType) bool {
		_, ok := c.types[t]
		return ok
	}
	configs := []telemetry.Configuration{}
	telemetry.GlobalClient.ProductChange(telemetry.NamespaceProfilers,
		true,
		append(configs, []telemetry.Configuration{
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
			{Name: "execution_trace_enabled", Value: c.traceEnabled},
			{Name: "execution_trace_period", Value: c.traceConfig.Period.String()},
			{Name: "execution_trace_size_limit", Value: c.traceConfig.Limit},
			{Name: "endpoint_count_enabled", Value: c.endpointCountEnabled},
		}...))
}
