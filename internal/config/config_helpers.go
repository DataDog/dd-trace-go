// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"fmt"
	"maps"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal"
	configtelemetry "github.com/DataDog/dd-trace-go/v2/internal/config/configtelemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/config/provider"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/samplingrules"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
)

// DefaultSocketDSDPath is the UDS socket path probed during DogStatsD
// auto-discovery. Exported as a var only for test overrides.
var DefaultSocketDSDPath = "/var/run/datadog/dsd.socket"

const (
	// DefaultRateLimit specifies the default rate limit per second for traces.
	// TODO: Maybe delete this. We will have defaults in supported_configuration.json anyway.
	DefaultRateLimit = 100.0

	// DefaultMaxTagsHeaderLen is the default value for DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH.
	DefaultMaxTagsHeaderLen = 512
	// MaxPropagatedTagsLength is the upper bound on DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH.
	MaxPropagatedTagsLength = 512
	// TraceMaxSize is the maximum number of spans we keep in memory for a
	// single trace. This is to avoid memory leaks. If more spans than this
	// are added to a trace, then the trace is dropped and the spans are
	// discarded. Adding additional spans after a trace is dropped does
	// nothing.
	TraceMaxSize = int(1e5)

	// Datadog trace protocol versions (agent wire format).
	TraceProtocolV04              = 0.4 // default
	TraceProtocolV1               = 1.0
	TraceProtocolVersionStringV04 = "0.4"
	TraceProtocolVersionStringV1  = "1.0"

	// Agent URL schemes supported by DD_TRACE_AGENT_URL.
	URLSchemeUnix  = "unix"
	URLSchemeHTTP  = "http"
	URLSchemeHTTPS = "https"

	DefaultStatsdPort = "8125"

	// Trace API paths appended to the agent URL for each protocol.
	TracesPathV04 = "/v0.4/traces"
	TracesPathV1  = "/v1.0/traces"

	// OTLP standard traces path and default collector port.
	otlpTracesPath  = "/v1/traces"
	otlpMetricsPath = "/v1/metrics"
	otlpDefaultPort = "4318"

	// OTLPContentTypeHeader is the Content-Type header value required for HTTP protobuf payloads.
	OTLPContentTypeHeader = "application/x-protobuf"

	// OTLPMetricsFlushInterval is the default cadence for flushing and exporting span metrics.
	OTLPMetricsFlushInterval = 10 * time.Second
)

func validateSampleRate(rate float64) bool {
	if rate < 0.0 || rate > 1.0 {
		log.Warn("ignoring DD_TRACE_SAMPLE_RATE: out of range %f", rate)
		return false
	}
	return true
}

func validateRateLimit(rate float64) bool {
	if rate < 0.0 {
		log.Warn("ignoring DD_TRACE_RATE_LIMIT: negative value %f", rate)
		return false
	}
	return true
}

func validateAgentTimeout(timeout int) bool {
	if timeout < 0 {
		log.Warn("ignoring DD_TRACE_AGENT_TIMEOUT: negative value %d", timeout)
		return false
	}
	return true
}

func validateSendRetries(retries int) bool {
	if retries < 0 {
		log.Warn("ignoring DD_TRACE_SEND_RETRIES: negative value %d", retries)
		return false
	}
	return true
}

// parseSpanAttributeSchema parses the DD_TRACE_SPAN_ATTRIBUTE_SCHEMA value.
// It accepts "v0", "v1" (case-insensitive) and returns the corresponding integer version.
// An empty string defaults to 0 (v0). Invalid values are rejected.
func parseSpanAttributeSchema(v string) (int, bool) {
	switch strings.ToLower(v) {
	case "", "v0":
		return 0, true
	case "v1":
		return 1, true
	default:
		log.Warn("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA=%s is not a valid value, ignoring", v)
		return 0, false
	}
}

// resolveMaxTagsHeaderLen normalises a DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH value
// into the supported range. Negative values are clamped to 0 (which disables
// tags propagation); values above MaxPropagatedTagsLength are clamped down.
func resolveMaxTagsHeaderLen(v int) int {
	if v < 0 {
		log.Warn("Invalid value %d for DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH. Setting to 0.", v)
		return 0
	}
	if v > MaxPropagatedTagsLength {
		log.Warn("Invalid value %d for DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH. Maximum allowed is %d. Setting to %d.", v, MaxPropagatedTagsLength, MaxPropagatedTagsLength)
		return MaxPropagatedTagsLength
	}
	return v
}

func validatePartialFlushMinSpans(minSpans int) bool {
	if minSpans <= 0 {
		log.Warn("ignoring DD_TRACE_PARTIAL_FLUSH_MIN_SPANS: negative value %d", minSpans)
		return false
	}
	if minSpans >= TraceMaxSize {
		log.Warn("ignoring DD_TRACE_PARTIAL_FLUSH_MIN_SPANS: value %d is greater than the max number of spans that can be kept in memory for a single trace (%d spans)", minSpans, TraceMaxSize)
		return false
	}
	return true
}

func validateTraceProtocolVersion(v string) bool {
	return v == TraceProtocolVersionStringV04 || v == TraceProtocolVersionStringV1
}

func resolveTraceProtocol(v string) float64 {
	if v == TraceProtocolVersionStringV1 {
		return TraceProtocolV1
	}
	return TraceProtocolV04
}

// resolveAgentURL computes the final agent URL from the three env-var strings
// read through the provider. The priority mirrors internal.AgentURLFromEnv:
//  1. DD_TRACE_AGENT_URL (if non-empty and valid)
//  2. DD_AGENT_HOST / DD_TRACE_AGENT_PORT (if either is non-empty)
//  3. DefaultTraceAgentUDSPath (if the socket file exists)
//  4. http://localhost:8126
func resolveAgentURL(agentURLStr, host, port string) *url.URL {
	if agentURLStr != "" {
		u, err := url.Parse(agentURLStr)
		if err == nil {
			switch u.Scheme {
			case URLSchemeUnix, URLSchemeHTTP, URLSchemeHTTPS:
				return u
			default:
				log.Warn("Unsupported protocol %q in Agent URL %q. Must be one of: %s, %s, %s.", u.Scheme, agentURLStr, URLSchemeHTTP, URLSchemeHTTPS, URLSchemeUnix)
			}
		} else {
			log.Warn("Failed to parse DD_TRACE_AGENT_URL: %s", err.Error())
		}
	}

	httpURL := buildHTTPURL(host, port)
	// If either the host or port is set, return the HTTP URL, else try to detect the UDS URL
	if host != "" || port != "" {
		return httpURL
	}
	if u := detectUDSURL(); u != nil {
		return u
	}
	return httpURL
}

func buildHTTPURL(host, port string) *url.URL {
	if host == "" {
		host = internal.DefaultAgentHostname
	}
	if port == "" {
		port = internal.DefaultTraceAgentPort
	}
	return &url.URL{
		Scheme: URLSchemeHTTP,
		Host:   net.JoinHostPort(host, port),
	}
}

func detectUDSURL() *url.URL {
	if _, err := os.Stat(internal.DefaultTraceAgentUDSPath); err != nil {
		return nil
	}
	return &url.URL{
		Scheme: URLSchemeUnix,
		Path:   internal.DefaultTraceAgentUDSPath,
	}
}

// initialDogstatsdURL builds the resolved DogStatsD URL from env inputs.
// Precedence: addr (DD_DOGSTATSD_URL) > host/port (DD_DOGSTATSD_HOST/PORT) >
// UDS auto-discovery > agentHost:DefaultStatsdPort. The returned URL is
// always complete: host+port for TCP, or unix scheme + path for UDS.
func initialDogstatsdURL(addr, host, port, agentHost, socketPath string) *url.URL {
	if addr != "" {
		return parseDogstatsdAddr(addr)
	}
	if host != "" || port != "" {
		if host == "" {
			host = agentHost
		}
		if host == "" {
			host = internal.DefaultAgentHostname
		}
		if port == "" {
			port = DefaultStatsdPort
		}
		return &url.URL{Host: net.JoinHostPort(host, port)}
	}
	if _, err := os.Stat(socketPath); err == nil {
		return &url.URL{Scheme: URLSchemeUnix, Path: socketPath}
	}
	host = agentHost
	if host == "" {
		host = internal.DefaultAgentHostname
	}
	return &url.URL{Host: net.JoinHostPort(host, DefaultStatsdPort)}
}

// parseDogstatsdAddr accepts "host:port" or "unix:///path/to/socket".
func parseDogstatsdAddr(addr string) *url.URL {
	if strings.HasPrefix(addr, "unix://") {
		if u, err := url.Parse(addr); err == nil {
			return u
		} else {
			log.Warn("Failed to parse DogStatsD unix address %q: %s", addr, err)
		}
	}
	return &url.URL{Host: addr}
}

// formatDogstatsdAddr renders the URL for NewStatsdClient.
func formatDogstatsdAddr(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.Scheme == URLSchemeUnix {
		return "unix://" + u.Path
	}
	return u.Host
}

// resolveOTLPTraceURL resolves the OTLP trace endpoint from OTEL_EXPORTER_OTLP_TRACES_ENDPOINT if set, else agentURL host + default OTLP port 4318 + /v1/traces.
// When the user-provided endpoint is set, it is validated: it must be a parseable URL with an http or https scheme.
// If validation fails, the default endpoint is used instead.
// parseAndValidateOTLPURL parses rawURL and validates that it uses http or https.
// Logs a warning and returns (nil, false) on failure.
func parseAndValidateOTLPURL(envVar, rawURL string) (*url.URL, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		log.Warn("Failed to parse %s %q: %s. Falling back to default.", envVar, rawURL, err.Error())
		return nil, false
	}
	if u.Scheme != URLSchemeHTTP && u.Scheme != URLSchemeHTTPS {
		log.Warn("Unsupported scheme %q in %s %q. Must be %s or %s. Falling back to default.", u.Scheme, envVar, rawURL, URLSchemeHTTP, URLSchemeHTTPS)
		return nil, false
	}
	return u, true
}

func resolveOTLPTraceURL(rawAgentURL *url.URL, otlpTracesEndpoint string) string {
	if otlpTracesEndpoint != "" {
		if _, ok := parseAndValidateOTLPURL("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", otlpTracesEndpoint); ok {
			return otlpTracesEndpoint
		}
	}
	host := internal.DefaultAgentHostname
	if rawAgentURL != nil {
		if h := rawAgentURL.Hostname(); h != "" {
			host = h
		}
	}
	return fmt.Sprintf("http://%s%s", net.JoinHostPort(host, otlpDefaultPort), otlpTracesPath)
}

// buildOTLPHeaders builds the OTLP headers map from the provided map.
// It adds the Content-Type header if not present.
func buildOTLPHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		headers = make(map[string]string)
	}
	headers["Content-Type"] = OTLPContentTypeHeader
	return headers
}

// samplingRulesFromSource resolves rules for key, falling back to the key+"_FILE" path
// when the inline value is empty; file-derived rules are still reported as OriginEnvVar.
func samplingRulesFromSource(p *provider.Provider, key string, spanType samplingrules.SamplingRuleType) ([]samplingrules.SamplingRule, telemetry.Origin) {
	raw, origin := p.GetStringWithOrigin(key, "")
	rulesFile := p.GetString(key+"_FILE", "")
	if raw != "" && rulesFile != "" {
		log.Warn("DIAGNOSTICS Error(s): %s is available and will take precedence over %s_FILE", key, key)
	} else if raw == "" && rulesFile != "" {
		b, err := os.ReadFile(rulesFile)
		if err != nil {
			log.Warn("DIAGNOSTICS Error(s): couldn't read file from %s_FILE: %s", key, err)
		} else {
			raw = string(b)
			origin = telemetry.OriginEnvVar
		}
	}
	rules, err := samplingrules.UnmarshalSamplingRules([]byte(raw), spanType)
	if err != nil {
		log.Warn("DIAGNOSTICS Error(s) parsing %s: %s", key, err)
	}
	return rules, origin
}

// samplingRulesBlockedByPrecedence reports whether a WithSamplingRules call (origin
// OriginCode) should be dropped because current already came from a non-default,
// non-code source — env/declarative config takes precedence per ddtrace/tracer/doc.go.
func samplingRulesBlockedByPrecedence(field string, current, incoming telemetry.Origin) bool {
	if incoming != telemetry.OriginCode || current == telemetry.OriginDefault || current == telemetry.OriginCode {
		return false
	}
	log.Warn("config: %s is already set via %s; ignoring WithSamplingRules", field, current)
	return true
}

// parseGlobalTags parses a DD_TAGS-style string into a tag map, dropping
// git-metadata tags so they don't leak onto every span. Returns nil when no
// usable tags remain.
func parseGlobalTags(v string) map[string]any {
	if v == "" {
		return nil
	}
	parsed := internal.ParseTagString(v)
	internal.CleanGitMetadataTags(parsed)
	if len(parsed) == 0 {
		return nil
	}
	tags := make(map[string]any, len(parsed))
	for k, val := range parsed {
		tags[k] = val
	}
	return tags
}

// reportGlobalTagTelemetry reports the per-key "global_tag_<key>" telemetry.
func reportGlobalTagTelemetry(key string, value any, origin telemetry.Origin) {
	configtelemetry.Report("global_tag_"+key, value, origin)
}

// resolveOTLPEndpoint returns the OTEL_EXPORTER_OTLP_ENDPOINT base URL, defaulting to http://<agent-host>:4318.
func resolveOTLPEndpoint(rawAgentURL *url.URL, endpoint string) string {
	if endpoint != "" {
		if _, ok := parseAndValidateOTLPURL("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint); ok {
			return endpoint
		}
	}
	host := internal.DefaultAgentHostname
	if rawAgentURL != nil {
		if h := rawAgentURL.Hostname(); h != "" {
			host = h
		}
	}
	return "http://" + net.JoinHostPort(host, otlpDefaultPort)
}

// resolveOTLPMetricsURL resolves the OTLP metrics endpoint; metricsEndpoint takes precedence over genericEndpoint.
func resolveOTLPMetricsURL(metricsEndpoint, genericEndpoint string) string {
	if metricsEndpoint != "" {
		if u, ok := parseAndValidateOTLPURL("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", metricsEndpoint); ok {
			if u.Path == "" || u.Path == "/" {
				u.Path = otlpMetricsPath
			}
			return u.String()
		}
	}
	u, _ := url.Parse(genericEndpoint) // already validated by resolveOTLPEndpoint
	u.Path = strings.TrimRight(u.Path, "/") + otlpMetricsPath
	return u.String()
}

// buildOTLPMetricsHeaders merges generic and signal-specific OTLP headers; signal headers take precedence.
func buildOTLPMetricsHeaders(genericHeaders, signalHeaders map[string]string) map[string]string {
	if len(genericHeaders) == 0 && len(signalHeaders) == 0 {
		return nil
	}
	merged := make(map[string]string, len(genericHeaders)+len(signalHeaders))
	maps.Copy(merged, genericHeaders)
	maps.Copy(merged, signalHeaders)
	return merged
}

// resolveOTLPMetricsProtocol normalises OTEL_EXPORTER_OTLP_METRICS_PROTOCOL to one of
// "http/json" or "http/protobuf". Unknown values and the empty string default to "http/protobuf"
// per the OTel specification.
func resolveOTLPMetricsProtocol(v string) string {
	if v == "http/json" {
		return "http/json"
	}
	return "http/protobuf"
}

// resolveOTLPMetricsFlushInterval parses _DD_TRACE_METRICS_OTEL_FLUSH_INTERVAL (milliseconds).
// The variable is internal and intended for tests only; in production it returns the default 10 s.
func resolveOTLPMetricsFlushInterval(raw string) time.Duration {
	if raw == "" {
		return OTLPMetricsFlushInterval
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms <= 0 {
		log.Warn("Invalid _DD_TRACE_METRICS_OTEL_FLUSH_INTERVAL %q; using default %s.", raw, OTLPMetricsFlushInterval)
		return OTLPMetricsFlushInterval
	}
	return time.Duration(ms) * time.Millisecond
}
