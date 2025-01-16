// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package newtelemetry

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal"
)

type ClientConfig struct {
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

	// FlushIntervalRange is the interval at which the client flushes the data.
	// By default, the client will start to Flush at 60s intervals and will reduce the interval based on the load till it hit 15s
	// Both values cannot be higher than 60s because the heartbeat need to be sent at least every 60s.
	FlushIntervalRange struct {
		Min time.Duration
		Max time.Duration
	}

	// Debug enables debug mode for the telemetry clientt and sent it to the backend so it logs the request
	Debug bool

	// APIKey is the API key to use for sending telemetry to the agentless endpoint. (using DD_API_KEY env var by default)
	APIKey string
}

const (
	// agentlessURL is the endpoint used to send telemetry in an agentless environment. It is
	// also the default URL in case connecting to the agent URL fails.
	agentlessURL = "https://instrumentation-telemetry-intake.datadoghq.com/api/v2/apmtelemetry"

	// defaultHeartbeatInterval is the default interval at which the agent sends a heartbeat.
	defaultHeartbeatInterval = 60.0 * time.Second

	// defaultMinFlushInterval is the default interval at which the client flushes the data.
	defaultMinFlushInterval = 15.0 * time.Second

	// defaultMaxFlushInterval is the default interval at which the client flushes the data.
	defaultMaxFlushInterval = 60.0 * time.Second

	agentProxyAPIPath = "/telemetry/proxy/api/v2/apmtelemetry"
)

// clamp squeezes a value between a minimum and maximum value.
func clamp[T ~int64](value, minVal, maxVal T) T {
	return max(min(maxVal, value), minVal)
}

func (config ClientConfig) validateConfig() error {
	if config.AgentlessURL == "" && config.AgentURL == "" {
		return errors.New("either AgentlessURL or AgentURL must be set")
	}

	if config.HeartbeatInterval > 60*time.Second {
		return fmt.Errorf("HeartbeatInterval cannot be higher than 60s, got %v", config.HeartbeatInterval)
	}

	if config.FlushIntervalRange.Min > 60*time.Second || config.FlushIntervalRange.Max > 60*time.Second {
		return fmt.Errorf("FlushIntervalRange cannot be higher than 60s, got Min: %v, Max: %v", config.FlushIntervalRange.Min, config.FlushIntervalRange.Max)
	}

	if config.FlushIntervalRange.Min > config.FlushIntervalRange.Max {
		return fmt.Errorf("FlushIntervalRange Min cannot be higher than Max, got Min: %v, Max: %v", config.FlushIntervalRange.Min, config.FlushIntervalRange.Max)
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

func (config ClientConfig) ToWriterConfig(tracerConfig internal.TracerConfig) (internal.WriterConfig, error) {
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

	return internal.WriterConfig{
		TracerConfig: tracerConfig,
		Endpoints:    endpoints,
		HTTPClient:   config.HTTPClient,
		Debug:        config.Debug,
	}, nil
}
