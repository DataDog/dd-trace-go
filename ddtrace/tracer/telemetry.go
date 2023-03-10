// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

// start the global instrumentation telemetry client with tracer data
func startTelemetry(c *config) {
	// need to re-intialize default values
	if !telemetry.GlobalClient.Started() {
		telemetry.GlobalClient.Default()
		telemetry.GlobalClient.ApplyOps(
			telemetry.WithService(c.serviceName),
			telemetry.WithEnv(c.env),
			telemetry.WithHTTPClient(c.httpClient),
			// c.logToStdout is true if serverless is turned o
			telemetry.WithURL(c.logToStdout, c.agentURL.String()),
			telemetry.WithVersion(c.version),
		)
	}
	telemetryConfigs := []telemetry.Configuration{
		{Name: "trace_debug_enabled", Value: c.debug},
		{Name: "agent_feature_drop_p0s", Value: c.agent.DropP0s},
		{Name: "stats_computation_enabled", Value: c.agent.Stats},
		{Name: "dogstatsd_port", Value: c.agent.StatsdPort},
		{Name: "lambda_mode", Value: c.logToStdout},
		{Name: "send_retries", Value: c.sendRetries},
		{Name: "trace_startup_logs_enabled", Value: c.logStartup},
		{Name: "service", Value: c.serviceName},
		{Name: "universal_version", Value: c.universalVersion},
		{Name: "env", Value: c.env},
		{Name: "agent_url", Value: c.agentURL.String()},
		{Name: "agent_hostname", Value: c.hostname},
		{Name: "runtime_metrics_enabled", Value: c.runtimeMetrics},
		{Name: "dogstatsd_addr", Value: c.dogstatsdAddr},
		{Name: "trace_debug_enabled", Value: c.noDebugStack},
		{Name: "profiling_hotspots_enabled", Value: c.profilerHotspots},
		{Name: "profiling_endpoints_enabled", Value: c.profilerEndpoints},
		{Name: "trace_enabled", Value: c.enabled},
	}
	for k, v := range c.featureFlags {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{Name: k, Value: v})
	}
	for k, v := range c.serviceMappings {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{Name: "service_mapping_" + k, Value: v})
	}
	for k, v := range c.globalTags {
		telemetryConfigs = append(telemetryConfigs, telemetry.Configuration{Name: "global_tag_" + k, Value: v})
	}
	for _, rule := range append(c.spanRules, c.traceRules...) {
		var service string
		var name string
		if rule.Service != nil {
			service = rule.Service.String()
		}
		if rule.Name != nil {
			name = rule.Name.String()
		}
		telemetryConfigs = append(telemetryConfigs,
			telemetry.Configuration{Name: fmt.Sprintf("sr_%s_(%s)_(%s)", rule.ruleType.String(), service, name),
				Value: fmt.Sprintf("rate:%f_maxPerSecond:%f", rule.Rate, rule.MaxPerSecond)})
	}
	telemetry.GlobalClient.Start(telemetryConfigs)
}

func stopTelemetry() {
	telemetry.GlobalClient.Stop()
}
