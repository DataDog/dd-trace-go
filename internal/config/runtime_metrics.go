// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package config

import (
	"maps"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/config/configtelemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/env"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

const runtimeMetricsPath = "/v1/metrics"

func (c *Config) loadRuntimeMetricsTransport() {
	p := provider.NewEnvironment()
	c.runtimeMetricsProtocol = strings.ToLower(strings.TrimSpace(p.GetString("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", p.GetString("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf"))))
	genericEndpoint := p.GetString("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	metricsEndpoint := p.GetString("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	c.runtimeMetricsHTTPEndpoint = "localhost:4318"
	c.runtimeMetricsGRPCEndpoint = "localhost:4317"
	c.runtimeMetricsHTTPPath = runtimeMetricsPath
	for i, raw := range []string{genericEndpoint, metricsEndpoint} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		c.runtimeMetricsHTTPEndpoint = u.Host
		c.runtimeMetricsGRPCEndpoint = path.Join(u.Host, u.Path)
		c.runtimeMetricsHTTPPath = path.Join(u.Path, runtimeMetricsPath)
		if i == 1 {
			c.runtimeMetricsHTTPPath = u.Path
			if u.Path == "" {
				c.runtimeMetricsHTTPPath = "/"
			}
		}
		c.runtimeMetricsInsecure = strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "unix")
	}
	for _, key := range []string{"OTEL_EXPORTER_OTLP_INSECURE", "OTEL_EXPORTER_OTLP_METRICS_INSECURE"} {
		if raw := strings.TrimSpace(p.GetString(key, "")); raw != "" {
			c.runtimeMetricsInsecure = strings.EqualFold(raw, "true")
		}
	}
	if genericEndpoint == "" && metricsEndpoint == "" {
		host := p.GetString("DD_AGENT_HOST", "localhost")
		insecure := true
		if u, err := url.Parse(p.GetString("DD_TRACE_AGENT_URL", "")); err == nil && u.Hostname() != "" {
			host = u.Hostname()
			insecure = u.Scheme == "http" || u.Scheme == "unix"
		}
		c.runtimeMetricsHTTPEndpoint = net.JoinHostPort(host, "4318")
		c.runtimeMetricsGRPCEndpoint = net.JoinHostPort(host, "4317")
		if insecure {
			c.runtimeMetricsInsecure = true
		}
	}
	c.runtimeMetricsHTTPPath = strings.TrimSpace(c.runtimeMetricsHTTPPath)
	if c.runtimeMetricsHTTPPath == "" || c.runtimeMetricsHTTPPath == "." {
		c.runtimeMetricsHTTPPath = runtimeMetricsPath
	} else if !path.IsAbs(c.runtimeMetricsHTTPPath) {
		c.runtimeMetricsHTTPPath = "/" + c.runtimeMetricsHTTPPath
	}
	for _, key := range []string{"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_METRICS_HEADERS"} {
		if raw := strings.TrimSpace(p.GetString(key, "")); raw != "" {
			c.runtimeMetricsHeaders = parseRuntimeMetricsHeaders(raw)
		}
	}
}

func parseRuntimeMetricsHeaders(raw string) map[string]string {
	headers := make(map[string]string)
	for entry := range strings.SplitSeq(raw, ",") {
		key, value, found := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !found || key == "" || strings.ContainsFunc(key, func(r rune) bool {
			return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r))
		}) {
			continue
		}
		value, err := url.PathUnescape(value)
		if err == nil {
			headers[key] = strings.TrimSpace(value)
		}
	}
	return headers
}

func (c *Config) RuntimeMetricsProtocol() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetricsProtocol
}

func (c *Config) RuntimeMetricsHTTPEndpoint() (endpoint, path string, insecure bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetricsHTTPEndpoint, c.runtimeMetricsHTTPPath, c.runtimeMetricsInsecure
}

func (c *Config) RuntimeMetricsGRPCEndpoint() (endpoint string, insecure bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetricsGRPCEndpoint, c.runtimeMetricsInsecure
}

func (c *Config) RuntimeMetricsHeaders() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return maps.Clone(c.runtimeMetricsHeaders)
}

func (c *Config) RuntimeMetricsEffectiveProtocol() string {
	if c.RuntimeMetricsProtocol() == "grpc" {
		return "grpc"
	}
	return "http/protobuf"
}

const (
	runtimeMetricsIntervalKey     = "OTEL_METRIC_EXPORT_INTERVAL"
	runtimeMetricsTimeoutKey      = "OTEL_METRIC_EXPORT_TIMEOUT"
	defaultRuntimeMetricsInterval = 10 * time.Second
	defaultRuntimeMetricsTimeout  = 7500 * time.Millisecond
	defaultSDKMetricsInterval     = 60 * time.Second
	defaultSDKMetricsTimeout      = 30 * time.Second
)

func (c *Config) loadRuntimeMetricsReader() {
	c.runtimeMetricsExportInterval, c.runtimeMetricsSDKInterval, c.runtimeMetricsReaderTelemetry[0] = resolveRuntimeMetricsDuration(runtimeMetricsIntervalKey, defaultRuntimeMetricsInterval, defaultSDKMetricsInterval)
	c.runtimeMetricsExportTimeout, c.runtimeMetricsSDKTimeout, c.runtimeMetricsReaderTelemetry[1] = resolveRuntimeMetricsDuration(runtimeMetricsTimeoutKey, defaultRuntimeMetricsTimeout, defaultSDKMetricsTimeout)
	c.runtimeMetricsTemporality = "delta"
	if strings.EqualFold(strings.TrimSpace(provider.NewEnvironment().GetString("OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE", "")), "cumulative") {
		c.runtimeMetricsTemporality = "cumulative"
	}
}

func resolveRuntimeMetricsDuration(key string, fallback, sdkFallback time.Duration) (time.Duration, time.Duration, telemetry.Configuration) {
	raw := env.Get(key)
	input := telemetry.Configuration{Name: key, Value: int(fallback.Milliseconds()), Origin: telemetry.OriginDefault}
	duration := fallback
	if milliseconds, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
		duration = time.Duration(milliseconds) * time.Millisecond
		input.Value = milliseconds
		input.Origin = telemetry.OriginEnvVar
	}
	// SDK environment fallback parses without trimming and checks positivity before conversion.
	if milliseconds, err := strconv.Atoi(raw); err == nil && milliseconds > 0 {
		sdkFallback = time.Duration(milliseconds) * time.Millisecond
	}
	if duration <= 0 {
		duration = sdkFallback
	}
	return duration, sdkFallback, input
}

func (c *Config) RuntimeMetricsExportInterval() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetricsExportInterval
}

func (c *Config) RuntimeMetricsExportTimeout() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetricsExportTimeout
}

func (c *Config) ResolveRuntimeMetricsExportInterval(interval time.Duration) time.Duration {
	if interval > 0 {
		return interval
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetricsSDKInterval
}

func (c *Config) ResolveRuntimeMetricsExportTimeout(timeout time.Duration) time.Duration {
	if timeout > 0 {
		return timeout
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetricsSDKTimeout
}

func (c *Config) RuntimeMetricsTemporalityPreference() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.runtimeMetricsTemporality
}

// ReportRuntimeMetricsReaderConfig reports input values when an enabled MeterProvider is created.
func (c *Config) ReportRuntimeMetricsReaderConfig() {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, input := range c.runtimeMetricsReaderTelemetry {
		if input.Origin == telemetry.OriginDefault {
			configtelemetry.ReportDefault(input.Name, input.Value)
		} else {
			configtelemetry.Report(input.Name, input.Value, input.Origin)
		}
	}
}
