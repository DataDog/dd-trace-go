// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion"
	"github.com/DataDog/dd-trace-go/v2/internal/osinfo"
	telemetrylog "github.com/DataDog/dd-trace-go/v2/internal/telemetry/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// orchestrionConfig is the JSON shape used to report Orchestrion configuration
// in the startup log. Values are sourced from the internal/orchestrion package
// (orchestrion.Enabled / orchestrion.Version), which are set at build time.
type (
	orchestrionConfig struct {
		// Enabled indicates whether this tracer was instantiated via Orchestrion.
		Enabled bool `json:"enabled"`

		// Metadata holds Orchestrion specific metadata (e.g orchestrion version, mode (toolexec or manual) etc..)
		Metadata *orchestrionMetadata `json:"metadata,omitempty"`
	}
	orchestrionMetadata struct {
		// Version is the version of the orchestrion tool that was used to instrument the application.
		Version string `json:"version,omitempty"`
	}
)

// startupInfo contains various information about the status of the tracer on startup.
type startupInfo struct {
	Date                        string                       `json:"date"`                           // ISO 8601 date and time of start
	OSName                      string                       `json:"os_name"`                        // Windows, Darwin, Debian, etc.
	OSVersion                   string                       `json:"os_version"`                     // Version of the OS
	Version                     string                       `json:"version"`                        // Tracer version
	Lang                        string                       `json:"lang"`                           // "Go"
	LangVersion                 string                       `json:"lang_version"`                   // Go version, e.g. go1.13
	Env                         string                       `json:"env"`                            // Tracer env
	Service                     string                       `json:"service"`                        // Tracer Service
	AgentURL                    string                       `json:"agent_url"`                      // The address of the agent
	AgentError                  string                       `json:"agent_error"`                    // Any error that occurred trying to connect to agent
	Debug                       bool                         `json:"debug"`                          // Whether debug mode is enabled
	AnalyticsEnabled            bool                         `json:"analytics_enabled"`              // True if there is a global analytics rate set
	SampleRate                  string                       `json:"sample_rate"`                    // The default sampling rate for the rules sampler
	SampleRateLimit             string                       `json:"sample_rate_limit"`              // The rate limit configured with the rules sampler
	TraceSamplingRules          []SamplingRule               `json:"trace_sampling_rules"`           // Trace rules used by the rules sampler
	SpanSamplingRules           []SamplingRule               `json:"span_sampling_rules"`            // Span rules used by the rules sampler
	SamplingRulesError          string                       `json:"sampling_rules_error"`           // Any errors that occurred while parsing sampling rules
	ServiceMappings             map[string]string            `json:"service_mappings"`               // Service Mappings
	Tags                        map[string]string            `json:"tags"`                           // Global tags
	RuntimeMetricsEnabled       bool                         `json:"runtime_metrics_enabled"`        // Whether runtime metrics are enabled
	RuntimeMetricsV2Enabled     bool                         `json:"runtime_metrics_v2_enabled"`     // Whether runtime metrics v2 are enabled
	ProfilerCodeHotspotsEnabled bool                         `json:"profiler_code_hotspots_enabled"` // Whether profiler code hotspots are enabled
	ProfilerEndpointsEnabled    bool                         `json:"profiler_endpoints_enabled"`     // Whether profiler endpoints are enabled
	ApplicationVersion          string                       `json:"dd_version"`                     // Version of the user's application
	Architecture                string                       `json:"architecture"`                   // Architecture of host machine
	GlobalService               string                       `json:"global_service"`                 // Global service string. If not-nil should be same as Service. (#614)
	LambdaMode                  string                       `json:"lambda_mode"`                    // Whether the client has enabled lambda mode
	AppSec                      bool                         `json:"appsec"`                         // AppSec status: true when started, false otherwise.
	AgentFeatures               agentFeatures                `json:"agent_features"`                 // Lists the capabilities of the agent.
	Integrations                map[string]integrationConfig `json:"integrations"`                   // Available tracer integrations
	PartialFlushEnabled         bool                         `json:"partial_flush_enabled"`          // Whether Partial Flushing is enabled
	PartialFlushMinSpans        int                          `json:"partial_flush_min_spans"`        // The min number of spans to trigger a partial flush
	Orchestrion                 orchestrionConfig            `json:"orchestrion"`                    // Orchestrion (auto-instrumentation) configuration.
	FeatureFlags                []string                     `json:"feature_flags"`
	PropagationStyleInject      string                       `json:"propagation_style_inject"`     // Propagation style for inject
	PropagationStyleExtract     string                       `json:"propagation_style_extract"`    // Propagation style for extract
	PropagationBehaviorExtract  string                       `json:"propagation_behavior_extract"` // Propagation behavior for extract
	TracingAsTransport          bool                         `json:"tracing_as_transport"`         // Whether the tracer is disabled and other products are using it as a transport
	DogstatsdAddr               string                       `json:"dogstatsd_address"`            // Destination of statsd payloads
	DataStreamsEnabled          bool                         `json:"data_streams_enabled"`         // Whether Data Streams is enabled

	// Effective OpenTelemetry configuration, keyed by the configuration name it
	// describes. Fields tagged omitempty are omitted when the setting does not
	// apply to an effective pipeline or has no faithful equivalent.
	OTelEnabled               bool `json:"OTEL_ENABLED"`                    // Whether any OTel pipeline or semantics is effective
	MetricsOTelEnabled        bool `json:"DD_METRICS_OTEL_ENABLED"`         // Resolved OTel metrics feature flag
	LogsOTelEnabled           bool `json:"DD_LOGS_OTEL_ENABLED"`            // Resolved OTel logs feature flag
	TraceOTelSemanticsEnabled bool `json:"DD_TRACE_OTEL_SEMANTICS_ENABLED"` // Resolved OTel semantics feature flag

	OTelTracesExporter  string            `json:"OTEL_TRACES_EXPORTER,omitempty"`                // "otlp" when traces are exported over OTLP
	OTLPTracesEndpoint  string            `json:"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT,omitempty"`  // Sanitized OTLP traces URL
	OTLPTracesProtocol  string            `json:"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL,omitempty"`  // OTLP traces transport
	OTLPTracesHeaders   map[string]string `json:"OTEL_EXPORTER_OTLP_TRACES_HEADERS,omitzero"`    // OTLP traces header names, values redacted
	OTLPMetricsEndpoint string            `json:"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT,omitempty"` // Sanitized OTLP metrics URL
	OTLPMetricsProtocol string            `json:"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL,omitempty"` // OTLP metrics transport
	OTLPMetricsHeaders  map[string]string `json:"OTEL_EXPORTER_OTLP_METRICS_HEADERS,omitzero"`   // OTLP metrics header names, values redacted

	OTelMetricExportInterval         int64  `json:"OTEL_METRIC_EXPORT_INTERVAL,omitempty"`                       // MeterProvider export interval, in milliseconds
	OTLPMetricsTemporalityPreference string `json:"OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE,omitempty"` // MeterProvider temporality preference

	OTelLogsExporter       string            `json:"OTEL_LOGS_EXPORTER,omitempty"`               // "otlp", the only logs exporter, when DD_LOGS_OTEL_ENABLED is set
	OTLPLogsEndpoint       string            `json:"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT,omitempty"` // Sanitized OTLP logs URL
	OTLPLogsProtocol       string            `json:"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL,omitempty"` // OTLP logs transport
	OTLPLogsHeaders        map[string]string `json:"OTEL_EXPORTER_OTLP_LOGS_HEADERS,omitzero"`   // OTLP logs header names, values redacted
	BLRPScheduleDelay      int64             `json:"OTEL_BLRP_SCHEDULE_DELAY,omitempty"`         // Logs batch processor export interval, in milliseconds
	BLRPExportTimeout      int64             `json:"OTEL_BLRP_EXPORT_TIMEOUT,omitempty"`         // Logs batch processor export timeout, in milliseconds
	BLRPMaxQueueSize       int               `json:"OTEL_BLRP_MAX_QUEUE_SIZE,omitempty"`         // Logs batch processor queue size
	BLRPMaxExportBatchSize int               `json:"OTEL_BLRP_MAX_EXPORT_BATCH_SIZE,omitempty"`  // Logs batch processor export batch size

	OTelLogLevel           string            `json:"OTEL_LOG_LEVEL,omitempty"`           // Minimum level of the tracer logger
	OTelServiceName        string            `json:"OTEL_SERVICE_NAME,omitempty"`        // Finalized service name
	OTelResourceAttributes map[string]string `json:"OTEL_RESOURCE_ATTRIBUTES,omitempty"` // Resource exported with OTLP traces
	OTelPropagators        string            `json:"OTEL_PROPAGATORS,omitempty"`         // Propagators, when inject and extract match
	OTelTracesSampler      string            `json:"OTEL_TRACES_SAMPLER,omitempty"`      // OTel equivalent of the trace sampler, or a datadog_* name
	OTelTracesSamplerArg   string            `json:"OTEL_TRACES_SAMPLER_ARG,omitempty"`  // Ratio of a traceidratio sampler
}

// checkEndpoint tries to connect to the URL specified by endpoint.
// If the endpoint is not reachable, checkEndpoint returns an error
// explaining why.
func checkEndpoint(c *http.Client, endpoint string, protocol float64, extraHeaders map[string]string) error {
	b := []byte{0x90} // empty array
	if protocol == traceProtocolV1 {
		b = []byte{0x80} // empty map
	}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("cannot create http request: %s", err)
	}
	req.Header.Set(traceCountHeader, "0")
	req.Header.Set("Content-Type", "application/msgpack")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	res, err := c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return nil
}

// logStartup generates a startupInfo for a tracer and writes it to the log in
// JSON format.
func logStartup(t *tracer) {
	tags := make(map[string]string)
	globalTags := t.config.internalConfig.GlobalTags()
	for k, v := range globalTags {
		tags[k] = fmt.Sprintf("%v", v)
	}

	allFeatures := t.config.internalConfig.FeatureFlags()
	featureFlags := make([]string, 0, len(allFeatures))
	for f := range allFeatures {
		featureFlags = append(featureFlags, f)
	}

	partialFlushEnabled, partialFlushMinSpans := t.config.internalConfig.PartialFlushEnabled()

	orchCfg := orchestrionConfig{Enabled: orchestrion.Enabled()}
	if orchestrion.Version != "" {
		orchCfg.Metadata = &orchestrionMetadata{Version: orchestrion.Version}
	}

	var injectorNames, extractorNames, extractBehavior string
	switch v := t.config.propagator.(type) {
	case *chainedPropagator:
		injectorNames = v.injectorNames
		extractorNames = v.extractorsNames
		extractBehavior = v.propagationBehaviorExtract
	case nil:
		injectorNames = ""
		extractorNames = ""
		extractBehavior = ""
	default:
		injectorNames = "custom"
		extractorNames = "custom"
		extractBehavior = "custom"
	}
	af := t.config.agent.load()
	proto := t.config.effectiveTraceProtocol()

	// Determine the agent URL to use in the logs.
	// Use the source URL from internalConfig for unix sockets (before UDS rewriting).
	var agentURL string
	if srcURL := t.config.internalConfig.RawAgentURL(); srcURL != nil && srcURL.Scheme == "unix" {
		agentURL = srcURL.String()
	} else {
		agentURL = t.config.ddTransport.endpoint(proto)
	}
	info := startupInfo{
		Date:                        time.Now().Format(time.RFC3339),
		OSName:                      osinfo.OSName(),
		OSVersion:                   osinfo.OSVersion(),
		Version:                     version.Tag,
		Lang:                        "Go",
		LangVersion:                 runtime.Version(),
		Env:                         t.config.internalConfig.Env(),
		Service:                     t.config.internalConfig.ServiceName(),
		AgentURL:                    agentURL,
		Debug:                       t.config.internalConfig.Debug(),
		AnalyticsEnabled:            !math.IsNaN(globalconfig.AnalyticsRate()),
		SampleRate:                  fmt.Sprintf("%f", t.rulesSampling.traces.globalRate),
		SampleRateLimit:             "disabled",
		TraceSamplingRules:          t.config.internalConfig.TraceSamplingRules(),
		SpanSamplingRules:           t.config.internalConfig.SpanSamplingRules(),
		ServiceMappings:             t.config.internalConfig.ServiceMappings(),
		Tags:                        tags,
		RuntimeMetricsEnabled:       t.config.internalConfig.RuntimeMetricsEnabled(),
		RuntimeMetricsV2Enabled:     t.config.internalConfig.RuntimeMetricsV2Enabled(),
		ApplicationVersion:          t.config.internalConfig.Version(),
		ProfilerCodeHotspotsEnabled: t.config.internalConfig.ProfilerHotspotsEnabled(),
		ProfilerEndpointsEnabled:    t.config.internalConfig.ProfilerEndpoints(),
		Architecture:                runtime.GOARCH,
		GlobalService:               globalconfig.ServiceName(),
		LambdaMode:                  strconv.FormatBool(t.config.internalConfig.LogToStdout()),
		AgentFeatures:               af,
		Integrations:                t.config.integrations,
		AppSec:                      appsec.Enabled(),
		PartialFlushEnabled:         partialFlushEnabled,
		PartialFlushMinSpans:        partialFlushMinSpans,
		Orchestrion:                 orchCfg,
		FeatureFlags:                featureFlags,
		PropagationStyleInject:      injectorNames,
		PropagationStyleExtract:     extractorNames,
		PropagationBehaviorExtract:  extractBehavior,
		TracingAsTransport:          t.config.tracingAsTransport,
		DogstatsdAddr:               t.config.internalConfig.DogstatsdAddr(),
		DataStreamsEnabled:          t.config.internalConfig.DataStreamsMonitoringEnabled(),
	}
	if limit, ok := t.rulesSampling.TraceRateLimit(); ok {
		info.SampleRateLimit = fmt.Sprintf("%v", limit)
	}
	t.addOTelStartupInfo(&info)
	if !t.config.internalConfig.LogToStdout() {
		startupHeaders := traceTransportHeaders(t.config.internalConfig)
		if err := checkEndpoint(t.config.httpClient, t.config.ddTransport.endpoint(proto), proto, startupHeaders); err != nil {
			info.AgentError = err.Error()
			log.Warn("DIAGNOSTICS Unable to reach agent intake: %s", err.Error())
		}
	}
	bs, err := json.Marshal(info)
	if err != nil {
		//nolint:gocritic // Diagnostic logging needs full struct representation
		log.Warn("DIAGNOSTICS Failed to serialize json for startup log (%v) %#v\n", err, info)
		return
	}
	log.Info("DATADOG TRACER CONFIGURATION %s\n", string(bs))
	telemetrylog.Debug("DATADOG TRACER CONFIGURATION", slog.String("config", string(bs)))
}

// redactedHeaderValue replaces every OTLP header value in the startup log.
const redactedHeaderValue = "REDACTED"

// conflictingOTLPValue replaces an OTLP setting that the effective exporters of
// a signal do not all set to the same value.
const conflictingOTLPValue = "CONFLICTING"

// addOTelStartupInfo adds the effective OpenTelemetry configuration to info.
// Every value comes from state resolved during tracer configuration; nothing
// is read from the environment here.
func (t *tracer) addOTelStartupInfo(info *startupInfo) {
	cfg := t.config.internalConfig
	if t.otlpExportMode {
		info.OTelTracesExporter = "otlp"
	}
	info.MetricsOTelEnabled = cfg.RuntimeMetricsOtelEnabled()
	info.LogsOTelEnabled = cfg.LogsOTelEnabled()
	info.TraceOTelSemanticsEnabled = cfg.OTelSemanticsEnabled()

	tracesOTLP := t.otlpTracesPipelineEnabled()
	metricsOTLP := t.config.otelRuntimeMetricsShouldBeEnabled
	semantics := cfg.OTelSemanticsEnabled() && cfg.TracingEnabled()
	// DD_LOGS_OTEL_ENABLED alone does not count: the tracer never starts the
	// OTel logs pipeline and cannot tell whether ddtrace/opentelemetry/log is
	// linked or started.
	spanMetricsOTLP := t.otlpSpanMetricsEnabled()
	info.OTelEnabled = tracesOTLP || metricsOTLP || semantics || spanMetricsOTLP

	if tracesOTLP {
		info.OTLPTracesEndpoint = sanitizeOTLPEndpoint(cfg.OTLPTraceURL())
		// The OTLP trace writer only sends protobuf over HTTP.
		info.OTLPTracesProtocol = "http/protobuf"
		headers := cfg.OTLPTraceHeaders()
		// The writer always sets Content-Type itself, overriding any configured value.
		delete(headers, "Content-Type")
		info.OTLPTracesHeaders = redactOTLPHeaders(headers)
	}
	// The OTel MeterProvider and the span metrics exporter resolve their
	// transport separately, so they can disagree on a setting.
	var metricsExporters []otlpExporterInfo
	if metricsOTLP {
		metricsExporters = append(metricsExporters, runtimeMetricsExporterInfo(cfg))
	}
	if spanMetricsOTLP {
		metricsExporters = append(metricsExporters, otlpExporterInfo{
			endpoint: sanitizeOTLPEndpoint(cfg.OTLPMetricsURL()),
			protocol: cfg.OTLPMetricsProtocol(),
			headers:  cfg.OTLPMetricsHeaders(),
		})
	}
	info.OTLPMetricsEndpoint, info.OTLPMetricsProtocol, info.OTLPMetricsHeaders = commonOTLPExporterInfo(metricsExporters)
	// Only the MeterProvider reads these: span metrics are always delta and
	// flushed on the stats interval.
	if metricsOTLP {
		info.OTelMetricExportInterval = cfg.RuntimeMetricsExportInterval().Milliseconds()
		info.OTLPMetricsTemporalityPreference = cfg.RuntimeMetricsTemporalityPreference()
	}
	// The tracer does not start the OTel logs pipeline: these describe the
	// configuration ddtrace/opentelemetry/log uses once started.
	if cfg.LogsOTelEnabled() {
		info.OTelLogsExporter = "otlp"
		info.OTLPLogsEndpoint, info.OTLPLogsProtocol = otlpLogsEndpoint(cfg)
		info.OTLPLogsHeaders = redactOTLPHeaders(cfg.OTLPLogsHeaders())
		info.BLRPScheduleDelay = cfg.BLRPScheduleDelay().Milliseconds()
		info.BLRPExportTimeout = cfg.BLRPExportTimeout().Milliseconds()
		info.BLRPMaxQueueSize = cfg.BLRPMaxQueueSize()
		// The batch processor caps the export batch size at the queue size.
		info.BLRPMaxExportBatchSize = min(cfg.BLRPMaxExportBatchSize(), info.BLRPMaxQueueSize)
	}

	// The logger only filters debug messages; info and above are always emitted.
	info.OTelLogLevel = "info"
	if log.DebugEnabled() {
		info.OTelLogLevel = "debug"
	}
	info.OTelServiceName = cfg.ServiceName()
	if w, ok := t.traceWriter.(*otlpTraceWriter); ok && tracesOTLP {
		info.OTelResourceAttributes = make(map[string]string, len(w.resource.Attributes))
		for _, kv := range w.resource.Attributes {
			info.OTelResourceAttributes[kv.Key] = kv.Value.GetStringValue()
		}
	}
	info.OTelPropagators = otelPropagators(t.config.propagator)
	info.OTelTracesSampler, info.OTelTracesSamplerArg = t.otelTracesSampler()
}

// otelPropagators returns the OTEL_PROPAGATORS equivalent of p. It returns ""
// when p or one of its propagators is custom, or when injection and extraction
// use different sequences, including when extraction is ignored, since no
// single OTEL_PROPAGATORS value describes them.
func otelPropagators(p Propagator) string {
	cp, ok := p.(*chainedPropagator)
	if !ok {
		return ""
	}
	if len(cp.injectors) == 0 && len(cp.extractors) == 0 {
		return "none"
	}
	inject, extract := otelPropagatorNames(cp.injectors), otelPropagatorNames(cp.extractors)
	if inject == "" || inject != extract || cp.propagationBehaviorExtract == propagationBehaviorExtractIgnore {
		return ""
	}
	return inject
}

// otelPropagatorNames returns the comma-separated OTel names of propagators,
// or "" when one of them has no OTel name.
func otelPropagatorNames(propagators []Propagator) string {
	names := make([]string, len(propagators))
	for i, p := range propagators {
		if names[i] = getPropagatorName(p); names[i] == "" {
			return ""
		}
	}
	return strings.Join(names, ",")
}

// otelTracesSampler returns the OTel sampler equivalent to the tracer's
// effective root sampling. Spans with a propagated sampling decision are never
// resampled, so every equivalent is parent-based. Datadog sampling without an
// OTel equivalent is reported with a datadog_* name describing what drives it;
// trace sampling rules use datadog_custom_rules, matching dd-trace-dotnet.
func (t *tracer) otelTracesSampler() (name, arg string) {
	// A custom sampler or a WithSamplerRate rate below 1 runs before rule-based
	// sampling and composes with it.
	rs, ok := t.config.sampler.(*rateSampler)
	if !ok {
		return "datadog_custom_sampler", ""
	}
	if rs.Rate() != 1 {
		return "datadog_sampler_rate", ""
	}
	rules := t.rulesSampling.traces
	rate, ok := rules.fixedRate()
	if !ok {
		if rules.enabled() {
			return "datadog_custom_rules", ""
		}
		if _, otel := t.defaultSampler.(*otelParentBasedAlwaysOnSampler); otel {
			return "parentbased_always_on", ""
		}
		return "datadog_agent_rates", ""
	}
	switch rate {
	case 0:
		return "parentbased_always_off", ""
	case 1:
		return "parentbased_always_on", ""
	}
	return "parentbased_traceidratio", strconv.FormatFloat(rate, 'f', -1, 64)
}

// sanitizeOTLPEndpoint returns endpoint without userinfo, query, or fragment so
// credentials cannot reach the startup log. It returns "" when endpoint cannot
// be parsed or is opaque or hostless, since it then cannot be sanitized.
func sanitizeOTLPEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || u.Opaque != "" || u.Host == "" {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// otlpExporterInfo is the transport of an OTLP exporter, with the endpoint
// sanitized and the header values not yet redacted.
type otlpExporterInfo struct {
	endpoint string
	protocol string
	headers  map[string]string
}

// commonOTLPExporterInfo returns the transport settings shared by every
// exporter, reporting conflictingOTLPValue for an endpoint, a protocol, or a
// header that the exporters do not all set to the same value.
func commonOTLPExporterInfo(exporters []otlpExporterInfo) (endpoint, protocol string, headers map[string]string) {
	if len(exporters) == 0 {
		return "", "", nil
	}
	first := exporters[0]
	endpoint, protocol, headers = first.endpoint, first.protocol, redactOTLPHeaders(first.headers)
	for _, e := range exporters[1:] {
		if e.endpoint != endpoint {
			endpoint = conflictingOTLPValue
		}
		if e.protocol != protocol {
			protocol = conflictingOTLPValue
		}
		for k, v := range e.headers {
			if fv, ok := first.headers[k]; !ok || fv != v {
				headers[k] = conflictingOTLPValue
			}
		}
		for k := range first.headers {
			if _, ok := e.headers[k]; !ok {
				headers[k] = conflictingOTLPValue
			}
		}
	}
	return endpoint, protocol, headers
}

// otlpScheme returns the URL scheme of an exporter that connects with or
// without TLS.
func otlpScheme(insecure bool) string {
	if insecure {
		return "http"
	}
	return "https"
}

// runtimeMetricsExporterInfo returns the transport of the OTel MeterProvider
// exporter, built the way ddtrace/opentelemetry/metric builds it, including
// its http/protobuf fallback for unsupported protocols.
func runtimeMetricsExporterInfo(cfg *internalconfig.Config) otlpExporterInfo {
	if cfg.RuntimeMetricsProtocol() == "grpc" {
		target, insecure := cfg.RuntimeMetricsGRPCEndpoint()
		return otlpExporterInfo{
			endpoint: sanitizeOTLPEndpoint(otlpScheme(insecure) + "://" + target),
			protocol: "grpc",
			headers:  cfg.RuntimeMetricsHeaders(),
		}
	}
	host, path, insecure := cfg.RuntimeMetricsHTTPEndpoint()
	return otlpExporterInfo{
		endpoint: sanitizeOTLPEndpoint((&url.URL{Scheme: otlpScheme(insecure), Host: host, Path: path}).String()),
		protocol: "http/protobuf",
		headers:  cfg.RuntimeMetricsHeaders(),
	}
}

// otlpLogsEndpoint returns the sanitized endpoint and the protocol of the OTLP
// logs exporter, built the way ddtrace/opentelemetry/log builds them. The
// scheme ignores OTEL_EXPORTER_OTLP_LOGS_INSECURE and OTEL_EXPORTER_OTLP_INSECURE:
// the tracer does not support them for logs, but the SDK applies them to an
// https agent URL and to a gRPC endpoint without a scheme.
func otlpLogsEndpoint(cfg *internalconfig.Config) (endpoint, protocol string) {
	if cfg.OTLPLogsProtocol() == "grpc" {
		target, insecure := cfg.OTLPLogsGRPCEndpoint()
		if target == "" {
			return "", "grpc"
		}
		return sanitizeOTLPEndpoint(otlpScheme(insecure) + "://" + target), "grpc"
	}
	if u := cfg.OTLPLogsHTTPURL(); u != "" {
		return sanitizeOTLPEndpoint(u), "http/protobuf"
	}
	host, path, insecure := cfg.OTLPLogsAgentHTTPEndpoint()
	return (&url.URL{Scheme: otlpScheme(insecure), Host: host, Path: path}).String(), "http/protobuf"
}

// redactOTLPHeaders returns headers with every value redacted. It never
// returns nil, so an effective exporter without headers is logged as {}.
func redactOTLPHeaders(headers map[string]string) map[string]string {
	redacted := make(map[string]string, len(headers))
	for k := range headers {
		redacted[k] = redactedHeaderValue
	}
	return redacted
}

// otlpTracesPipelineEnabled reports whether spans are exported by the OTLP
// trace writer. OTEL_TRACES_EXPORTER=otlp alone is not enough: CI Visibility
// and log-to-stdout writers take precedence, and disabled tracing exports
// nothing.
func (t *tracer) otlpTracesPipelineEnabled() bool {
	return t.otlpExportMode && t.config.internalConfig.TracingEnabled()
}

// otlpSpanMetricsEnabled reports whether the stats concentrator exports span
// metrics over OTLP.
func (t *tracer) otlpSpanMetricsEnabled() bool {
	c, ok := t.stats.(*concentrator)
	if !ok {
		return false
	}
	_, ok = c.sender.(*otlpStatsSender)
	return ok
}
