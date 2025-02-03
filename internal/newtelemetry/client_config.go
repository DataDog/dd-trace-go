// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"time"

	globalinternal "gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
)

type Range[T any] struct {
	Min T
	Max T
}

type ClientConfig struct {
	// DependencyLoader determines how dependency data is sent via telemetry.
	// If nil, the library should not send the app-dependencies-loaded event.
	// The default value is [debug.ReadBuildInfo] since Application Security Monitoring uses this data to detect vulnerabilities in the ASM-SCA product
	// This can be controlled via the env var DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED
	DependencyLoader func() (*debug.BuildInfo, bool)

	// MetricsEnabled determines whether metrics are sent via telemetry.
	// If false, libraries should not send the generate-metrics or distributions events.
	// This can be controlled via the env var DD_TELEMETRY_METRICS_ENABLED
	MetricsEnabled bool

	// LogsEnabled determines whether logs are sent via telemetry.
	// This can be controlled via the env var DD_TELEMETRY_LOG_COLLECTION_ENABLED
	LogsEnabled bool

	// InternalMetricsEnabled determines whether client stats metrics are sent via telemetry.
	InternalMetricsEnabled bool

	// AgentlessURL is the full URL to the agentless telemetry endpoint. (optional)
	// Defaults to https://instrumentation-telemetry-intake.datadoghq.com/api/v2/apmtelemetry
	AgentlessURL string

	// AgentURL is the url of the agent to send telemetry to. (optional)
	// If the AgentURL is not set, the telemetry client will not attempt to connect to the agent before sending to the agentless endpoint.
	AgentURL string

	// HTTPClient is the http client to use for sending telemetry, defaults to a http.DefaultClient copy.
	HTTPClient *http.Client

	// HeartbeatInterval is the interval at which to send a heartbeat payload, defaults to 60s.
	// The maximum value is 60s.
	HeartbeatInterval time.Duration

	// ExtendedHeartbeatInterval is the interval at which to send an extended heartbeat payload, defaults to 24h.
	ExtendedHeartbeatInterval time.Duration

	// FlushInterval is the interval at which the client flushes the data.
	// By default, the client will start to Flush at 60s intervals and will reduce the interval based on the load till it hit 15s
	// Both values cannot be higher than 60s because the heartbeat need to be sent at least every 60s.
	FlushInterval Range[time.Duration]

	// PayloadQueueSize is the size of the payload queue.
	PayloadQueueSize Range[int]

	// DistributionsSize is the size of the distribution queue.
	DistributionsSize Range[int]

	// Debug enables debug mode for the telemetry clientt and sent it to the backend so it logs the request
	Debug bool

	// APIKey is the API key to use for sending telemetry to the agentless endpoint. (using DD_API_KEY env var by default)
	APIKey string

	// EarlyFlushPayloadSize is the size of the payload that will trigger an early flush.
	// This is necessary because backend won't allow bodies larger than 5MB.
	// The default value here will be 2MB to take into account the large inaccuracy in estimating the size of bodies
	EarlyFlushPayloadSize int
}

var (
	// agentlessURL is the endpoint used to send telemetry in an agentless environment. It is
	// also the default URL in case connecting to the agent URL fails.
	agentlessURL = "https://instrumentation-telemetry-intake.datadoghq.com/api/v2/apmtelemetry"

	// defaultHeartbeatInterval is the default interval at which the agent sends a heartbeat.
	defaultHeartbeatInterval = 60 // seconds

	// defaultExtendedHeartbeatInterval is the default interval at which the agent sends an extended heartbeat.
	defaultExtendedHeartbeatInterval = 24 * time.Hour

	// defaultMinFlushInterval is the default interval at which the client flushes the data.
	defaultMinFlushInterval = 15.0 * time.Second

	// defaultMaxFlushInterval is the default interval at which the client flushes the data.
	defaultMaxFlushInterval = 60.0 * time.Second

	agentProxyAPIPath = "/telemetry/proxy/api/v2/apmtelemetry"

	defaultEarlyFlushPayloadSize = 2 * 1024 * 1024 // 2MB

	// maxPayloadSize is specified by the backend to be 5MB. The goal is to never reach this value otherwise our data will be silently dropped.
	maxPayloadSize = 5 * 1024 * 1024 // 5MB

	// TODO: tweak this value once we get real telemetry data from the telemetry client
	// This means that, by default, we incur dataloss if we spend ~30mins without flushing, considering we send telemetry data this looks reasonable.
	// This also means that in the worst case scenario, memory-wise, the app is stabilized after running for 30mins.
	// Ideally both values should be power of 2 because of the way the ring queue is implemented as it's growing
	defaultPayloadQueueSize = Range[int]{
		Min: 4,
		Max: 32,
	}

	// TODO: tweak this value once we get telemetry data from the telemetry client
	// Default max size is a 2^14 array of float64 (2^3 bytes) which makes a distribution 128KB bytes array _at worse_.
	// Considering we add a point per user request on a simple http server, we would be losing data after 2^14 requests per minute or about 280 requests per second or under 3ms per request.
	// If this throughput is constant, the telemetry client flush ticker speed will increase to, at best, double twice to flush 15 seconds of data each time.
	// Which will bring our max throughput to 1100 points per second or about 750µs per request.
	distributionsSize = Range[int]{
		Min: 1 << 8,
		Max: 1 << 14,
	}
)

// clamp squeezes a value between a minimum and maximum value.
func clamp[T ~int64](value, minVal, maxVal T) T {
	return max(min(maxVal, value), minVal)
}

func (config ClientConfig) validateConfig() error {
	if config.HeartbeatInterval > 60*time.Second {
		return fmt.Errorf("HeartbeatInterval cannot be higher than 60s, got %v", config.HeartbeatInterval)
	}

	if config.FlushInterval.Min > 60*time.Second || config.FlushInterval.Max > 60*time.Second {
		return fmt.Errorf("FlushIntervalRange cannot be higher than 60s, got Min: %v, Max: %v", config.FlushInterval.Min, config.FlushInterval.Max)
	}

	if config.FlushInterval.Min > config.FlushInterval.Max {
		return fmt.Errorf("FlushIntervalRange Min cannot be higher than Max, got Min: %v, Max: %v", config.FlushInterval.Min, config.FlushInterval.Max)
	}

	if config.EarlyFlushPayloadSize > maxPayloadSize || config.EarlyFlushPayloadSize <= 0 {
		return fmt.Errorf("EarlyFlushPayloadSize must be between 0 and 5MB, got %v", config.EarlyFlushPayloadSize)
	}

	return nil
}

// defaultConfig returns a ClientConfig with default values set.
func defaultConfig(config ClientConfig) ClientConfig {
	if config.AgentlessURL == "" {
		config.AgentlessURL = agentlessURL
	}

	if config.APIKey == "" {
		config.APIKey = os.Getenv("DD_API_KEY")
	}

	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = time.Duration(globalinternal.IntEnv("DD_TELEMETRY_HEARTBEAT_INTERVAL", defaultHeartbeatInterval)) * time.Second
	} else {
		config.HeartbeatInterval = clamp(config.HeartbeatInterval, time.Microsecond, 60*time.Second)
	}

	if config.FlushInterval.Min == 0 {
		config.FlushInterval.Min = defaultMinFlushInterval
	} else {
		config.FlushInterval.Min = clamp(config.FlushInterval.Min, time.Microsecond, 60*time.Second)
	}

	if config.FlushInterval.Max == 0 {
		config.FlushInterval.Max = defaultMaxFlushInterval
	} else {
		config.FlushInterval.Max = clamp(config.FlushInterval.Max, time.Microsecond, 60*time.Second)
	}

	if config.DependencyLoader == nil && globalinternal.BoolEnv("DD_TELEMETRY_DEPENDENCY_COLLECTION_ENABLED", true) {
		config.DependencyLoader = debug.ReadBuildInfo
	}

	if !config.MetricsEnabled {
		config.MetricsEnabled = globalinternal.BoolEnv("DD_TELEMETRY_METRICS_ENABLED", true)
	}

	if !config.LogsEnabled {
		config.LogsEnabled = globalinternal.BoolEnv("DD_TELEMETRY_LOG_COLLECTION_ENABLED", true)
	}

	if !config.InternalMetricsEnabled {
		config.InternalMetricsEnabled = true
	}

	if config.EarlyFlushPayloadSize == 0 {
		config.EarlyFlushPayloadSize = defaultEarlyFlushPayloadSize
	}

	if config.ExtendedHeartbeatInterval == 0 {
		config.ExtendedHeartbeatInterval = defaultExtendedHeartbeatInterval
	}

	if config.PayloadQueueSize.Min == 0 {
		config.PayloadQueueSize.Min = defaultPayloadQueueSize.Min
	}

	if config.PayloadQueueSize.Max == 0 {
		config.PayloadQueueSize.Max = defaultPayloadQueueSize.Max
	}

	if config.DistributionsSize.Min == 0 {
		config.DistributionsSize.Min = distributionsSize.Min
	}

	if config.DistributionsSize.Max == 0 {
		config.DistributionsSize.Max = distributionsSize.Max
	}

	return config
}

func newWriterConfig(config ClientConfig, tracerConfig internal.TracerConfig) (internal.WriterConfig, error) {
	endpoints := make([]*http.Request, 0, 2)
	if config.AgentURL != "" {
		baseURL, err := url.Parse(config.AgentURL)
		if err != nil {
			return internal.WriterConfig{}, fmt.Errorf("invalid agent URL: %v", err)
		}

		baseURL.Path = agentProxyAPIPath
		request, err := http.NewRequest(http.MethodPost, baseURL.String(), nil)
		if err != nil {
			return internal.WriterConfig{}, fmt.Errorf("failed to create request: %v", err)
		}

		endpoints = append(endpoints, request)
	}

	if config.AgentlessURL != "" && config.APIKey != "" {
		request, err := http.NewRequest(http.MethodPost, config.AgentlessURL, nil)
		if err != nil {
			return internal.WriterConfig{}, fmt.Errorf("failed to create request: %v", err)
		}

		request.Header.Set("DD-API-KEY", config.APIKey)
		endpoints = append(endpoints, request)
	}

	if len(endpoints) == 0 {
		return internal.WriterConfig{}, fmt.Errorf("telemetry: could not build any endpoint, please provide an AgentURL or an APIKey with an optional AgentlessURL")
	}

	return internal.WriterConfig{
		TracerConfig: tracerConfig,
		Endpoints:    endpoints,
		HTTPClient:   config.HTTPClient,
		Debug:        config.Debug,
	}, nil
}
