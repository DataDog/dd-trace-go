// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"fmt"
	"maps"
	"net"
	"net/url"
	"path"
	"strings"
	"time"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const (
	// Default OTLP HTTP endpoint for Datadog
	defaultOTLPEndpoint = "http://localhost:4318"
	defaultOTLPPath     = "/v1/metrics"
	defaultOTLPPort     = "4318"
	defaultOTLPProtocol = "http/protobuf"

	// OTLP environment variables
	envOTLPEndpoint           = "OTEL_EXPORTER_OTLP_ENDPOINT"
	envOTLPMetricsEndpoint    = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
	envOTLPProtocol           = "OTEL_EXPORTER_OTLP_PROTOCOL"
	envOTLPMetricsProtocol    = "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"
	envOTLPMetricsTemporality = "OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE"

	// DD environment variables for agent configuration
	envDDTraceAgentURL  = "DD_TRACE_AGENT_URL"
	envDDAgentHost      = "DD_AGENT_HOST"
	envDDTraceAgentPort = "DD_TRACE_AGENT_PORT"

	// Telemetry tag values for protocol and encoding
	protocolHTTP     = "http"
	protocolGRPC     = "grpc"
	encodingProtobuf = "protobuf"
)

// telemetryExporter wraps a metric.Exporter to track export attempts and successes.
type telemetryExporter struct {
	metric.Exporter
	telemetry *MetricsExportTelemetry
}

// Export implements metric.Exporter.
func (e *telemetryExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	e.telemetry.RecordAttempt()
	err := e.Exporter.Export(ctx, rm)
	if err == nil {
		e.telemetry.RecordSuccess()
	}
	return err
}

// newDatadogOTLPExporter creates an OTLP exporter (HTTP or gRPC) configured with Datadog-specific defaults.
//
// Protocol selection priority:
// 1. OTEL_EXPORTER_OTLP_METRICS_PROTOCOL
// 2. OTEL_EXPORTER_OTLP_PROTOCOL
// 3. Default: http/protobuf
//
// Supported protocols:
// - "http/protobuf" or "http": HTTP with protobuf encoding
// - "grpc": gRPC
//
// Endpoint resolution priority:
// 1. OTEL_EXPORTER_OTLP_METRICS_ENDPOINT (highest priority)
// 2. OTEL_EXPORTER_OTLP_ENDPOINT
// 3. DD_TRACE_AGENT_URL hostname with appropriate port
// 4. DD_AGENT_HOST with appropriate port
// 5. localhost with default port (default)
func newDatadogOTLPExporter(ctx context.Context, httpOpts []otlpmetrichttp.Option, grpcOpts []otlpmetricgrpc.Option) (metric.Exporter, error) {
	return newDatadogOTLPExporterFromSnapshot(
		ctx,
		internalconfig.ResolveOTelMetricSnapshot(),
		httpOpts,
		grpcOpts,
	)
}

func newDatadogOTLPExporterFromSnapshot(
	ctx context.Context,
	snapshot internalconfig.OTelMetricSnapshot,
	httpOpts []otlpmetrichttp.Option,
	grpcOpts []otlpmetricgrpc.Option,
) (metric.Exporter, error) {
	// Determine protocol
	protocol := snapshot.Protocol

	var exporter metric.Exporter
	var err error
	var protocolTag, encodingTag string

	switch protocol {
	case protocolGRPC:
		exporter, err = newDatadogOTLPGRPCExporterFromSnapshot(ctx, snapshot, grpcOpts...)
		protocolTag = protocolGRPC
		encodingTag = encodingProtobuf
	case defaultOTLPProtocol, protocolHTTP:
		exporter, err = newDatadogOTLPHTTPExporterFromSnapshot(ctx, snapshot, httpOpts...)
		protocolTag = protocolHTTP
		encodingTag = encodingProtobuf
	default:
		log.Warn("Unknown OTLP protocol %q, defaulting to %s", protocol, defaultOTLPProtocol)
		exporter, err = newDatadogOTLPHTTPExporterFromSnapshot(ctx, snapshot, httpOpts...)
		protocolTag = protocolHTTP
		encodingTag = encodingProtobuf
	}

	if err != nil {
		return nil, err
	}

	// Wrap the exporter with telemetry tracking
	return &telemetryExporter{
		Exporter:  exporter,
		telemetry: NewMetricsExportTelemetry(protocolTag, encodingTag),
	}, nil
}

// otlpProtocol returns the OTLP protocol from environment variables.
// Priority: OTEL_EXPORTER_OTLP_METRICS_PROTOCOL > OTEL_EXPORTER_OTLP_PROTOCOL > "http/protobuf"
func otlpProtocol() string {
	return internalconfig.ResolveOTelMetricSnapshot().Protocol
}

// newDatadogOTLPHTTPExporter creates an OTLP HTTP exporter configured with Datadog-specific defaults.
func newDatadogOTLPHTTPExporter(ctx context.Context, opts ...otlpmetrichttp.Option) (metric.Exporter, error) {
	return newDatadogOTLPHTTPExporterFromSnapshot(ctx, internalconfig.ResolveOTelMetricSnapshot(), opts...)
}

func newDatadogOTLPHTTPExporterFromSnapshot(
	ctx context.Context,
	snapshot internalconfig.OTelMetricSnapshot,
	opts ...otlpmetrichttp.Option,
) (metric.Exporter, error) {
	// Build exporter options with DD defaults
	exporterOpts := buildHTTPExporterOptionsFromSnapshot(snapshot, opts...)

	// Create the OTLP HTTP exporter
	exporter, err := otlpmetrichttp.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP HTTP metrics exporter: %w", err)
	}

	return exporter, nil
}

// newDatadogOTLPGRPCExporter creates an OTLP gRPC exporter configured with Datadog-specific defaults.
func newDatadogOTLPGRPCExporter(ctx context.Context, opts ...otlpmetricgrpc.Option) (metric.Exporter, error) {
	return newDatadogOTLPGRPCExporterFromSnapshot(ctx, internalconfig.ResolveOTelMetricSnapshot(), opts...)
}

func newDatadogOTLPGRPCExporterFromSnapshot(
	ctx context.Context,
	snapshot internalconfig.OTelMetricSnapshot,
	opts ...otlpmetricgrpc.Option,
) (metric.Exporter, error) {
	// Build exporter options with DD defaults
	exporterOpts := buildGRPCExporterOptionsFromSnapshot(snapshot, opts...)

	// Create the OTLP gRPC exporter
	exporter, err := otlpmetricgrpc.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP gRPC metrics exporter: %w", err)
	}

	return exporter, nil
}

// buildHTTPExporterOptions constructs the OTLP HTTP exporter options with DD-specific defaults
func buildHTTPExporterOptions(userOpts ...otlpmetrichttp.Option) []otlpmetrichttp.Option {
	return buildHTTPExporterOptionsFromSnapshot(
		internalconfig.ResolveOTelMetricSnapshot(),
		userOpts...,
	)
}

func buildHTTPExporterOptionsFromSnapshot(
	snapshot internalconfig.OTelMetricSnapshot,
	userOpts ...otlpmetrichttp.Option,
) []otlpmetrichttp.Option {
	opts := []otlpmetrichttp.Option{
		// Set retry configuration
		otlpmetrichttp.WithRetry(datadogRetryConfig()),
		// Set timeout
		otlpmetrichttp.WithTimeout(snapshot.ExporterTimeout),
		// Set delta temporality as default (Datadog preference)
		otlpmetrichttp.WithTemporalitySelector(deltaTemporalitySelectorFromSnapshot(snapshot)),
	}

	if hasOTLPEndpointInSnapshot(snapshot) {
		endpointURL := metricHTTPEndpointURLFromSnapshot(snapshot)
		insecure := metricOTLPEndpointInsecureFromSnapshot(snapshot)
		opts = append(opts, otlpmetrichttp.WithEndpointURL(
			metricEndpointURLWithInsecure(endpointURL, insecure),
		))
	} else {
		endpoint, urlPath, ddInsecure := resolveOTLPEndpointHTTPFromSnapshot(snapshot)
		insecure := metricDDInsecureFromSnapshot(snapshot, ddInsecure)
		endpointURL := url.URL{Host: endpoint, Path: urlPath}
		opts = append(opts, otlpmetrichttp.WithEndpointURL(
			metricEndpointURLWithInsecure(endpointURL.String(), insecure),
		))
	}
	opts = append(opts, otlpmetrichttp.WithHeaders(maps.Clone(snapshot.Headers)))

	// Add user-provided options last so they can override defaults
	opts = append(opts, userOpts...)

	return opts
}

// buildGRPCExporterOptions constructs the OTLP gRPC exporter options with DD-specific defaults
func buildGRPCExporterOptions(userOpts ...otlpmetricgrpc.Option) []otlpmetricgrpc.Option {
	return buildGRPCExporterOptionsFromSnapshot(
		internalconfig.ResolveOTelMetricSnapshot(),
		userOpts...,
	)
}

func buildGRPCExporterOptionsFromSnapshot(
	snapshot internalconfig.OTelMetricSnapshot,
	userOpts ...otlpmetricgrpc.Option,
) []otlpmetricgrpc.Option {
	opts := []otlpmetricgrpc.Option{
		// Set timeout
		otlpmetricgrpc.WithTimeout(snapshot.ExporterTimeout),
		// Set delta temporality as default (Datadog preference)
		otlpmetricgrpc.WithTemporalitySelector(deltaTemporalitySelectorFromSnapshot(snapshot)),
		// Set retry config
		otlpmetricgrpc.WithRetry(datadogGRPCRetryConfig()),
	}

	if hasOTLPEndpointInSnapshot(snapshot) {
		endpointURL, endpoint := metricGRPCEndpointFromSnapshot(snapshot)
		insecure := metricOTLPEndpointInsecureFromSnapshot(snapshot)
		opts = append(opts, otlpmetricgrpc.WithEndpointURL(
			metricEndpointURLWithInsecure(endpointURL, insecure),
		))
		opts = append(opts, otlpmetricgrpc.WithEndpoint(endpoint))
	} else {
		endpoint, ddInsecure := resolveOTLPEndpointGRPCFromSnapshot(snapshot)
		insecure := metricDDInsecureFromSnapshot(snapshot, ddInsecure)
		endpointURL := url.URL{Host: endpoint}
		opts = append(opts, otlpmetricgrpc.WithEndpointURL(
			metricEndpointURLWithInsecure(endpointURL.String(), insecure),
		))
		opts = append(opts, otlpmetricgrpc.WithEndpoint(endpoint))
	}
	opts = append(opts, otlpmetricgrpc.WithHeaders(maps.Clone(snapshot.Headers)))

	// Add user-provided options last so they can override defaults
	opts = append(opts, userOpts...)

	return opts
}

// hasOTLPEndpointInEnv checks if OTLP endpoint is configured via OTEL environment variables
func hasOTLPEndpointInEnv() bool {
	return hasOTLPEndpointInSnapshot(internalconfig.ResolveOTelMetricSnapshot())
}

func hasOTLPEndpointInSnapshot(snapshot internalconfig.OTelMetricSnapshot) bool {
	return snapshot.Signal.Endpoint.Value != "" || snapshot.Generic.Endpoint.Value != ""
}

func metricHTTPEndpointURLFromSnapshot(snapshot internalconfig.OTelMetricSnapshot) string {
	if endpoint, signalSpecific, ok := parsedMetricEndpoint(snapshot); ok {
		if signalSpecific {
			if endpoint.Path == "" {
				endpoint.Path = "/"
			}
		} else {
			endpoint.Path = path.Join(endpoint.Path, defaultOTLPPath)
		}
		return endpoint.String()
	}
	return "https://localhost:4318" + defaultOTLPPath
}

func metricGRPCEndpointFromSnapshot(snapshot internalconfig.OTelMetricSnapshot) (string, string) {
	if endpoint, _, ok := parsedMetricEndpoint(snapshot); ok {
		return endpoint.String(), path.Join(endpoint.Host, endpoint.Path)
	}
	const defaultGRPCEndpoint = "localhost:4317"
	return "https://" + defaultGRPCEndpoint, defaultGRPCEndpoint
}

func parsedMetricEndpoint(snapshot internalconfig.OTelMetricSnapshot) (*url.URL, bool, bool) {
	var selected *url.URL
	if raw := strings.TrimSpace(snapshot.Generic.Endpoint.Value); raw != "" {
		if endpoint, err := url.Parse(raw); err == nil {
			selected = endpoint
		}
	}
	signalSpecific := false
	if raw := strings.TrimSpace(snapshot.Signal.Endpoint.Value); raw != "" {
		if endpoint, err := url.Parse(raw); err == nil {
			selected = endpoint
			signalSpecific = true
		}
	}
	return selected, signalSpecific, selected != nil
}

func metricOTLPEndpointInsecureFromSnapshot(snapshot internalconfig.OTelMetricSnapshot) bool {
	insecure := false
	if endpoint, _, ok := parsedMetricEndpoint(snapshot); ok {
		switch strings.ToLower(endpoint.Scheme) {
		case "http", "unix":
			insecure = true
		}
	}
	return applyMetricInsecureSnapshot(snapshot, insecure)
}

func metricDDInsecureFromSnapshot(snapshot internalconfig.OTelMetricSnapshot, ddInsecure bool) bool {
	insecure := applyMetricInsecureSnapshot(snapshot, false)
	if ddInsecure {
		// Datadog's explicit insecure fallback was historically applied after
		// the SDK environment, so it wins when the DD endpoint uses plaintext.
		return true
	}
	return insecure
}

func applyMetricInsecureSnapshot(snapshot internalconfig.OTelMetricSnapshot, insecure bool) bool {
	for _, setting := range []internalconfig.OTelRawSetting{
		snapshot.Generic.Insecure,
		snapshot.Signal.Insecure,
	} {
		value := strings.TrimSpace(setting.Value)
		if value != "" {
			// Match the OTel SDK: only "true" selects insecure; every other
			// non-empty value explicitly selects secure.
			insecure = strings.ToLower(value) == "true"
		}
	}
	return insecure
}

func metricEndpointURLWithInsecure(endpointURL string, insecure bool) string {
	endpoint, err := url.Parse(endpointURL)
	if err != nil {
		return endpointURL
	}
	if insecure {
		endpoint.Scheme = "http"
	} else {
		endpoint.Scheme = "https"
	}
	return endpoint.String()
}

// resolveOTLPEndpointHTTP determines the OTLP HTTP endpoint from DD agent configuration.
// Returns (endpoint, path, insecure) where:
// - endpoint is the host:port (e.g., "localhost:4318")
// - path is the URL path (e.g., "/v1/metrics")
// - insecure indicates whether to use http (true) or https (false)
//
// Priority order:
// 1. DD_TRACE_AGENT_URL with port changed to 4318
// 2. DD_AGENT_HOST:4318
// 3. localhost:4318 (default)
func resolveOTLPEndpointHTTP() (endpoint, path string, insecure bool) {
	return resolveOTLPEndpointHTTPFromSnapshot(internalconfig.ResolveOTelMetricSnapshot())
}

func resolveOTLPEndpointHTTPFromSnapshot(
	snapshot internalconfig.OTelMetricSnapshot,
) (endpoint, path string, insecure bool) {
	path = defaultOTLPPath
	insecure = true // default to http

	// Check DD_TRACE_AGENT_URL first
	if agentURL := snapshot.AgentURL; agentURL != "" {
		u, err := url.Parse(agentURL)
		if err != nil {
			log.Warn("Failed to parse DD_TRACE_AGENT_URL for metrics: %s, using default", err.Error())
		} else {
			// Extract hostname from the agent URL and use port 4318
			hostname := u.Hostname()
			if hostname != "" {
				endpoint = net.JoinHostPort(hostname, defaultOTLPPort)
				// Preserve the scheme from DD_TRACE_AGENT_URL
				insecure = (u.Scheme == "http" || u.Scheme == "unix")
				log.Debug("Using OTLP metrics endpoint from DD_TRACE_AGENT_URL: %s", endpoint)
				return
			}
		}
	}

	// Check DD_AGENT_HOST
	if host := snapshot.AgentHost; host != "" {
		endpoint = net.JoinHostPort(host, defaultOTLPPort)
		insecure = true
		return
	}

	// Default to localhost:4318
	endpoint = "localhost:4318"
	insecure = true
	return
}

// resolveOTLPEndpointGRPC determines the OTLP gRPC endpoint from DD agent configuration.
// Returns (endpoint, insecure) where:
// - endpoint is the host:port (e.g., "localhost:4317")
// - insecure indicates whether to use grpc (true) or grpcs (false)
//
// Priority order:
// 1. DD_TRACE_AGENT_URL with port changed to 4317
// 2. DD_AGENT_HOST:4317
// 3. localhost:4317 (default)
func resolveOTLPEndpointGRPC() (endpoint string, insecure bool) {
	return resolveOTLPEndpointGRPCFromSnapshot(internalconfig.ResolveOTelMetricSnapshot())
}

func resolveOTLPEndpointGRPCFromSnapshot(
	snapshot internalconfig.OTelMetricSnapshot,
) (endpoint string, insecure bool) {
	insecure = true // default to grpc (not grpcs)
	const defaultGRPCPort = "4317"

	// Check DD_TRACE_AGENT_URL first
	if agentURL := snapshot.AgentURL; agentURL != "" {
		u, err := url.Parse(agentURL)
		if err != nil {
			log.Warn("Failed to parse DD_TRACE_AGENT_URL for metrics: %s, using default", err.Error())
		} else {
			// Extract hostname from the agent URL and use port 4317 for gRPC
			hostname := u.Hostname()
			if hostname != "" {
				endpoint = net.JoinHostPort(hostname, defaultGRPCPort)
				// Preserve the scheme from DD_TRACE_AGENT_URL
				insecure = (u.Scheme == "http" || u.Scheme == "unix")
				log.Debug("Using OTLP gRPC metrics endpoint from DD_TRACE_AGENT_URL: %s", endpoint)
				return
			}
		}
	}

	// Check DD_AGENT_HOST
	if host := snapshot.AgentHost; host != "" {
		endpoint = net.JoinHostPort(host, defaultGRPCPort)
		log.Debug("Using OTLP gRPC metrics endpoint from DD_AGENT_HOST: %s", endpoint)
		return
	}

	// Default to localhost:4317
	endpoint = net.JoinHostPort("localhost", defaultGRPCPort)
	log.Debug("Using default OTLP gRPC metrics endpoint: %s", endpoint)
	return
}

// datadogGRPCRetryConfig returns the retry configuration for OTLP gRPC exporter.
func datadogGRPCRetryConfig() otlpmetricgrpc.RetryConfig {
	return otlpmetricgrpc.RetryConfig{
		Enabled:         true,
		InitialInterval: 5 * time.Second,
		MaxInterval:     30 * time.Second,
		MaxElapsedTime:  5 * time.Minute,
	}
}

// datadogRetryConfig returns a retry configuration that matches Datadog requirements
// The OTLP exporter will automatically retry on 429, 502, 503, 504 and honor Retry-After headers
func datadogRetryConfig() otlpmetrichttp.RetryConfig {
	return otlpmetrichttp.RetryConfig{
		Enabled:         true,
		InitialInterval: 1 * time.Second,
		MaxInterval:     30 * time.Second,
		MaxElapsedTime:  5 * time.Minute,
	}
}
