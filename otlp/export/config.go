// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"net/http"
	"time"
)

const (
	defaultSite             = "datadoghq.com"
	defaultMaxAttempts uint = 3

	pathTraces  = "/v1/traces"
	pathMetrics = "/v1/metrics"
	pathLogs    = "/v1/logs"

	headerContentType = "Content-Type"
	contentTypeProto  = "application/x-protobuf"
	headerAPIKey      = "dd-api-key"

	// headerMetricConfig pins Datadog's OTLP metric intake to emit exponential
	// histograms as distributions (DDSketch percentiles). Metrics + Datadog
	// route only.
	headerMetricConfig        = "dd-otel-metric-config"
	metricConfigDistributions = `{"histograms":{"mode":"distributions"}}`
)

// Config configures an OTLP export client. A client targets exactly one
// destination and signal; build several clients for multi-destination export.
//
// Routing:
//   - Datadog route (default): leave Endpoint empty and set Site + APIKey. The
//     client derives https://otlp.<site>/v1/<signal> and injects the dd-api-key
//     header.
//   - Collector/Agent route: set Endpoint to a base OTLP URL (the client
//     appends /v1/<signal>). No Datadog auth is injected unless APIKey is also
//     set (for a Datadog-compatible endpoint override).
type Config struct {
	// Site is the Datadog site (e.g. "datadoghq.com"). Defaults to datadoghq.com.
	// It is ignored when Endpoint is set.
	Site string
	// APIKey is the Datadog API key. Required for the Datadog route; when set it
	// injects the dd-api-key header regardless of Endpoint.
	APIKey string
	// Endpoint is a base OTLP URL (scheme://host[:port]); the client appends the
	// signal path. When empty, the endpoint is derived from Site. When set, it
	// takes precedence over Site (the collector/Agent route).
	Endpoint string

	// HTTPClient overrides the default HTTP client.
	HTTPClient *http.Client
	// Headers are extra request headers, applied last (they override defaults).
	Headers map[string]string
	// MaxAttempts bounds the total number of HTTP attempts per request, including
	// the first (default 3, minimum 1). Set to 1 to disable retries.
	MaxAttempts uint
	// RequestTimeout bounds each individual HTTP attempt. When >0 it is applied to
	// every attempt. When 0, a 10s default is applied only if the caller's context
	// has no deadline of its own, so a caller passing a longer ctx deadline (for a
	// large export or a slow collector) is not silently shortened.
	RequestTimeout time.Duration
}
