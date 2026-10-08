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
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
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
