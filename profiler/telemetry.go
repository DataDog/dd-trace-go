// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
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
	telemetry.ProductStarted(telemetry.NamespaceProfilers)
	telemetry.RegisterAppConfigs(telemetryConfiguration(c)...)
	if telemetry.GlobalClient() == nil {
		client, err := telemetry.NewClient(c.service, c.env, c.version, telemetry.ClientConfig{
			HTTPClient: c.httpClient,
			APIKey:     c.apiKey,
			AgentURL:   c.agentURL,
		})
		if err != nil {
			log.Debug("profiler: failed to create telemetry client: %s", err.Error())
			return
		}
		telemetry.StartApp(client)
	}
}

func telemetryConfiguration(c *config) []telemetry.Configuration {
	profileEnabled := func(t ProfileType) bool {
		_, ok := c.types[t]
		return ok
	}
	return []telemetry.Configuration{
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
		{Name: "flush_on_exit", Value: c.flushOnExit},
		{Name: "debug_compression_settings", Value: c.compressionConfig},
	}
}
