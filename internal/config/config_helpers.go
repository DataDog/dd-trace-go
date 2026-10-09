// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package config

import (
	"cmp"
	"maps"
	"math"
	"net"
	"net/url"
	"os"
	"path"
	"slices"
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

// The OTLP protocols each exporter supports, default first.
var (
	otlpMetricsProtocols = []string{defaultOTLPProtocol, "http/json"}
	otlpLogsProtocols    = []string{defaultOTLPProtocol, "http", "grpc"}
)

const (
	// DefaultRateLimit specifies the default rate limit per second for traces.
	// TODO: Maybe delete this. We will have defaults in supported_configuration.json anyway.
	DefaultRateLimit = 100.0

	// DefaultMaxTagsHeaderLen is the default value for DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH.
	DefaultMaxTagsHeaderLen = 512
	// defaultStatsAdditionalTagsCardinalityLimit is the default per-bucket cap for additional metric tag cardinality.
	defaultStatsAdditionalTagsCardinalityLimit = 100
	// maxAdditionalTagKeys is the maximum number of configured additional metric tag keys.
	maxAdditionalTagKeys = 6
	// defaultStatsWholeKeyCardinalityLimit is the default whole-key cardinality cap per bucket.
	defaultStatsWholeKeyCardinalityLimit = 2048
	// defaultStatsResourceCardinalityLimit is the default per-field cap for resource cardinality.
	defaultStatsResourceCardinalityLimit = 1024
	// defaultStatsHTTPEndpointCardinalityLimit is the default per-field cap for http_endpoint cardinality.
	defaultStatsHTTPEndpointCardinalityLimit = 512
	// defaultStatsPeerTagsCardinalityLimit is the default per-field cap for peer_tags cardinality.
	defaultStatsPeerTagsCardinalityLimit = 512
	// defaultStatsOriginCardinalityLimit is the default per-field cap for origin cardinality.
	defaultStatsOriginCardinalityLimit = 20
	// defaultBLRPMaxQueueSize is the default value for OTEL_BLRP_MAX_QUEUE_SIZE.
	defaultBLRPMaxQueueSize = 2048
	// defaultBLRPScheduleDelay is the default value for OTEL_BLRP_SCHEDULE_DELAY.
	defaultBLRPScheduleDelay = time.Second
	// defaultBLRPExportTimeout is the default value for OTEL_BLRP_EXPORT_TIMEOUT.
	defaultBLRPExportTimeout = 30 * time.Second
	// defaultBLRPMaxExportBatchSize is the default value for OTEL_BLRP_MAX_EXPORT_BATCH_SIZE.
	defaultBLRPMaxExportBatchSize = 512
	// defaultOTLPProtocol is the default value for OTEL_EXPORTER_OTLP_PROTOCOL.
	defaultOTLPProtocol = "http/protobuf"
	// defaultOTLPTimeout is the default value for OTEL_EXPORTER_OTLP_TIMEOUT.
	defaultOTLPTimeout = 10 * time.Second
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
	// otlpDefaultGRPCPort is the default OTLP gRPC collector port.
	otlpDefaultGRPCPort = "4317"

	// defaultRuntimeMetricsExportInterval is the default value for OTEL_METRIC_EXPORT_INTERVAL.
	defaultRuntimeMetricsExportInterval = 10 * time.Second
	// defaultRuntimeMetricsExportTimeout is the default value for OTEL_METRIC_EXPORT_TIMEOUT.
	defaultRuntimeMetricsExportTimeout = 7500 * time.Millisecond

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

// validateFeatureFlagsAgentlessPollInterval rejects rather than clamps: clamping would
// silently move a misconfigured billed-polling interval to a valid one.
func validateFeatureFlagsAgentlessPollInterval(seconds int) bool {
	if seconds <= 0 || seconds > 3600 {
		log.Warn("ignoring DD_FEATURE_FLAGS_CONFIGURATION_SOURCE_AGENTLESS_POLL_INTERVAL_SECONDS: value %d out of range (0, 3600]", seconds)
		return false
	}
	return true
}

// validateFeatureFlagsAgentlessRequestTimeout caps the upper bound because a larger value
// overflows int64 as a nanosecond duration, and http.Client reads a negative Timeout as
// "no timeout".
func validateFeatureFlagsAgentlessRequestTimeout(seconds int) bool {
	if seconds <= 0 || seconds > 300 {
		log.Warn("ignoring DD_FEATURE_FLAGS_CONFIGURATION_SOURCE_AGENTLESS_REQUEST_TIMEOUT_SECONDS: value %d out of range (0, 300]", seconds)
		return false
	}
	return true
}

// maxFlaggingProviderInitTimeoutMs is the largest value that still converts to a
// time.Duration without overflowing int64 into a negative duration.
const maxFlaggingProviderInitTimeoutMs = math.MaxInt64 / int64(time.Millisecond)

// maxDurationSeconds is the largest whole-second value that converts to a
// time.Duration without overflowing.
const maxDurationSeconds = float64(math.MaxInt64 / int64(time.Second))

// validateFlaggingProviderInitTimeout rejects an overflow-prone value so the caller falls
// back to the default rather than Init receiving an already-expired context.
func validateFlaggingProviderInitTimeout(ms int) bool {
	if ms <= 0 || int64(ms) > maxFlaggingProviderInitTimeoutMs {
		log.Warn("ignoring DD_EXPERIMENTAL_FLAGGING_PROVIDER_INITIALIZATION_TIMEOUT_MS: value %d out of range (0, %d]", ms, maxFlaggingProviderInitTimeoutMs)
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

func capAdditionalTagKeys(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	unique := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		unique = append(unique, tag)
	}
	if len(unique) > maxAdditionalTagKeys {
		dropped := unique[maxAdditionalTagKeys:]
		log.Warn("DD_TRACE_STATS_ADDITIONAL_TAGS is limited to %d keys; dropping configured tag keys: %s", maxAdditionalTagKeys, strings.Join(dropped, ","))
		unique = unique[:maxAdditionalTagKeys]
	}
	slices.Sort(unique)
	return unique
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

// TraceProtocolVersionString is the inverse of resolveTraceProtocol: it renders
// a protocol float64 back into the wire-version string reported to config
// telemetry, so DD_TRACE_AGENT_PROTOCOL_VERSION is always reported with a
// consistent type regardless of which source set it.
func TraceProtocolVersionString(v float64) string {
	if v == TraceProtocolV1 {
		return TraceProtocolVersionStringV1
	}
	return TraceProtocolVersionStringV04
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

// resolveOTLPTraceURL resolves the OTLP traces endpoint. tracesEndpoint takes
// precedence and is used as-is, per the OTel spec. /v1/traces is only appended to
// genericEndpoint, which must already be resolved (see resolveOTLPEndpoint).
// Both must be empty or valid http(s) URLs.
func resolveOTLPTraceURL(tracesEndpoint, genericEndpoint string) string {
	if tracesEndpoint != "" {
		return tracesEndpoint
	}
	u, _ := url.Parse(genericEndpoint) // already validated by resolveOTLPEndpoint
	return u.JoinPath(otlpTracesPath).String()
}

// buildOTLPTraceHeaders sets the Content-Type required for OTLP trace export.
func buildOTLPTraceHeaders(headers map[string]string) map[string]string {
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

// resolveOTLPEndpoint returns endpoint, an empty or valid http(s) URL, defaulting to http://<agent-host>:4318.
func resolveOTLPEndpoint(rawAgentURL *url.URL, endpoint string) string {
	if endpoint != "" {
		return endpoint
	}
	host := internal.DefaultAgentHostname
	if rawAgentURL != nil {
		if h := rawAgentURL.Hostname(); h != "" {
			host = h
		}
	}
	return "http://" + net.JoinHostPort(host, otlpDefaultPort)
}

// resolveOTLPMetricsURL uses metricsEndpoint as-is, else appends /v1/metrics to genericEndpoint.
// Both must be empty or valid http(s) URLs.
func resolveOTLPMetricsURL(metricsEndpoint, genericEndpoint string) string {
	if metricsEndpoint != "" {
		return metricsEndpoint
	}
	u, _ := url.Parse(genericEndpoint) // already validated by resolveOTLPEndpoint
	return u.JoinPath(otlpMetricsPath).String()
}

// getOTLPGenericValues returns every configured value of key, ordered from lowest
// to highest source priority, so each signal can pick the last one it supports.
func getOTLPGenericValues(p *provider.Provider, key string) []string {
	var values []string
	p.GetStringWithValidator(key, "", func(v string) bool {
		values = append(values, v)
		return true
	})
	return values
}

// resolveOTLPSetting returns the usable value of the signal-specific key and the
// last usable genericValues entry, read from genericKey; either may be empty.
// Every source is validated so an unusable value never hides a lower-priority one.
func resolveOTLPSetting(p *provider.Provider, key, genericKey string, genericValues []string, valid func(string) bool, warn bool) (specific, generic string) {
	specific = p.GetStringWithValidator(key, "", otlpValidator(key, valid, warn))
	// The generic value is only worth a warning when the signal-specific one is not used.
	validGeneric := otlpValidator(genericKey, valid, warn && specific == "")
	for _, v := range genericValues {
		if v != "" && validGeneric(v) {
			generic = v
		}
	}
	return specific, generic
}

// otlpValidator wraps valid to warn about unusable values of key. Set warn only when
// the exporter is enabled: other OpenTelemetry components may accept values this
// exporter rejects (e.g. grpc for span metrics).
func otlpValidator(key string, valid func(string) bool, warn bool) func(string) bool {
	return func(v string) bool {
		if valid(v) {
			return true
		}
		// The value is not logged: headers and endpoints may carry credentials.
		if warn {
			log.Warn("Unsupported %s value; falling back to default.", key)
		}
		return false
	}
}

// resolveOTLPHeaders returns a copy of the signal-specific OTLP headers,
// or of the generic headers when the signal-specific ones are empty.
func resolveOTLPHeaders(genericHeaders, signalHeaders map[string]string) map[string]string {
	if len(signalHeaders) > 0 {
		return maps.Clone(signalHeaders)
	}
	return maps.Clone(genericHeaders)
}

// resolveOTLPProtocol returns the lowercased signal-specific protocol, else the generic one, else the default.
func resolveOTLPProtocol(specific, generic string) string {
	return normalizeOTLPProtocol(cmp.Or(specific, generic, defaultOTLPProtocol))
}

// supportsOTLPProtocol returns a validator accepting the given protocols, case-insensitively.
func supportsOTLPProtocol(protocols []string) func(string) bool {
	return func(v string) bool {
		return slices.Contains(protocols, normalizeOTLPProtocol(v))
	}
}

func normalizeOTLPProtocol(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

// isOTLPHTTPURL reports whether v is an http or https URL.
func isOTLPHTTPURL(v string) bool {
	u, err := url.Parse(v)
	return err == nil && (u.Scheme == URLSchemeHTTP || u.Scheme == URLSchemeHTTPS)
}

// isURL reports whether v parses as a URL. The logs exporter does not require
// http or https because its gRPC transport also takes grpc:// endpoints.
func isURL(v string) bool {
	_, err := url.Parse(v)
	return err == nil
}

// parseOTLPHeaders parses comma-separated key=value pairs with percent-decoded
// values, dropping invalid entries without erasing an earlier valid duplicate.
func parseOTLPHeaders(str string) map[string]string {
	var headers map[string]string
	for entry := range strings.SplitSeq(str, ",") {
		key, val, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || !validHeaderFieldName(key) {
			continue
		}
		decoded, err := url.PathUnescape(strings.TrimSpace(val))
		if err != nil {
			continue
		}
		if headers == nil {
			headers = make(map[string]string)
		}
		headers[key] = decoded
	}
	return headers
}

// validHeaderFieldName reports whether s is a non-empty HTTP header field name.
func validHeaderFieldName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !internal.IsHTTPTokenChar(s[i]) {
			return false
		}
	}
	return true
}

// resolveRuntimeMetricsEndpoint resolves the runtime metrics HTTP host and path, gRPC target, and
// insecure flag. A signal endpoint is used as-is; a generic one gets otlpMetricsPath appended.
func resolveRuntimeMetricsEndpoint(genericEndpoint, metricsEndpoint, insecureOverride string, agentURL *url.URL) (httpHost, httpPath, grpcTarget string, insecure bool) {
	httpHost = net.JoinHostPort(internal.DefaultAgentHostname, otlpDefaultPort)
	grpcTarget = net.JoinHostPort(internal.DefaultAgentHostname, otlpDefaultGRPCPort)
	httpPath = otlpMetricsPath
	for i, raw := range []string{genericEndpoint, metricsEndpoint} {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		httpHost = u.Host
		grpcTarget = path.Join(u.Host, u.Path)
		httpPath = path.Join(u.Path, otlpMetricsPath)
		isSignalEndpoint := i == 1
		if isSignalEndpoint {
			httpPath = cmp.Or(u.Path, "/")
		}
		insecure = strings.EqualFold(u.Scheme, URLSchemeHTTP) || strings.EqualFold(u.Scheme, URLSchemeUnix)
	}
	// Like the OTel Go SDK, and unlike the OTel spec, an explicit setting overrides the endpoint scheme.
	if insecureOverride != "" {
		insecure = strings.EqualFold(insecureOverride, "true")
	}
	if genericEndpoint == "" && metricsEndpoint == "" {
		host := internal.DefaultAgentHostname
		agentInsecure := true
		if agentURL != nil {
			host = cmp.Or(agentURL.Hostname(), host)
			agentInsecure = agentURL.Scheme != URLSchemeHTTPS
		}
		httpHost = net.JoinHostPort(host, otlpDefaultPort)
		grpcTarget = net.JoinHostPort(host, otlpDefaultGRPCPort)
		insecure = insecure || agentInsecure
	}
	httpPath = strings.TrimSpace(httpPath)
	if httpPath == "" || httpPath == "." {
		httpPath = otlpMetricsPath
	} else if !path.IsAbs(httpPath) {
		httpPath = "/" + httpPath
	}
	return httpHost, httpPath, grpcTarget, insecure
}

// resolveRuntimeMetricsHeaders parses the signal headers when set, otherwise the generic ones.
// Entries with an invalid header name or value encoding are dropped.
func resolveRuntimeMetricsHeaders(genericHeaders, metricsHeaders string) map[string]string {
	return parseOTLPHeaders(cmp.Or(strings.TrimSpace(metricsHeaders), strings.TrimSpace(genericHeaders)))
}

// validateRuntimeMetricsTemporality returns true for the two supported temporality preferences.
// The warning is only logged when warn is true.
func validateRuntimeMetricsTemporality(v string, warn bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "delta", "cumulative":
		return true
	}
	if warn {
		log.Warn("Unsupported OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE %q; must be delta or cumulative. Falling back to delta.", v)
	}
	return false
}

// resolveOTelSignalEnabled returns whether an OTel SDK signal is enabled.
// OTEL_SDK_DISABLED disables every OTel SDK signal, so unlike other OTEL_*
// mappings it overrides an explicit DD opt-in.
func resolveOTelSignalEnabled(key string, enabled, sdkDisabled bool) bool {
	if !enabled || !sdkDisabled {
		return enabled
	}
	log.Warn("OTEL_SDK_DISABLED=true overrides %s=true", key)
	configtelemetry.Report(key, false, telemetry.OriginCalculated)
	return false
}

// resolveOTLPMetricsFlushInterval parses _DD_TRACE_STATS_INTERVAL (milliseconds).
// The variable is internal and intended for tests only; in production it returns the default 10 s.
func resolveOTLPMetricsFlushInterval(raw string) time.Duration {
	if raw == "" {
		return OTLPMetricsFlushInterval
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms <= 0 {
		log.Warn("Invalid _DD_TRACE_STATS_INTERVAL %q; using default %s.", raw, OTLPMetricsFlushInterval)
		return OTLPMetricsFlushInterval
	}
	return time.Duration(ms) * time.Millisecond
}
