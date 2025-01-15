// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"time"
)

var (
	// agentlessURL is the endpoint used to send telemetry in an agentless environment. It is
	// also the default URL in case connecting to the agent URL fails.
	agentlessURL = "https://instrumentation-telemetry-intake.datadoghq.com/api/v2/apmtelemetry"

	// defaultHeartbeatInterval is the default interval at which the agent sends a heartbeat.
	defaultHeartbeatInterval = 60.0 * time.Second

	// defaultMinFlushInterval is the default interval at which the client flushes the data.
	defaultMinFlushInterval = 15.0 * time.Second

	// defaultMaxFlushInterval is the default interval at which the client flushes the data.
	defaultMaxFlushInterval = 60.0 * time.Second
)

// clamp squeezes a value between a minimum and maximum value.
func clamp[T ~int64](value, minVal, maxVal T) T {
	return max(min(maxVal, value), minVal)
}

// defaultConfig returns a ClientConfig with default values set.
func defaultConfig(config ClientConfig) ClientConfig {
	if config.AgentlessURL == "" {
		config.AgentlessURL = agentlessURL
	}

	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = defaultHeartbeatInterval
	} else {
		config.HeartbeatInterval = clamp(config.HeartbeatInterval, time.Microsecond, 60*time.Second)
	}

	if config.FlushIntervalRange.Min == 0 {
		config.FlushIntervalRange.Min = defaultMinFlushInterval
	} else {
		config.FlushIntervalRange.Min = clamp(config.FlushIntervalRange.Min, time.Microsecond, 60*time.Second)
	}

	if config.FlushIntervalRange.Max == 0 {
		config.FlushIntervalRange.Max = defaultMaxFlushInterval
	} else {
		config.FlushIntervalRange.Max = clamp(config.FlushIntervalRange.Max, time.Microsecond, 60*time.Second)
	}

	return config
}
