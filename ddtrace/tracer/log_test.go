// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"math"
	"regexp"
	"testing"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const logPrefixRegexp = `Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+(-((rc|beta)\.[0-9]+|dev(\.\d+)?))?`

// otelStartupFieldsRegexp matches the optional OpenTelemetry startup fields
// without asserting their presence or values; system-tests cover them.
const otelStartupFieldsRegexp = `(,"(?:OTEL_ENABLED|DD_METRICS_OTEL_ENABLED|DD_LOGS_OTEL_ENABLED|DD_TRACE_OTEL_SEMANTICS_ENABLED|` +
	`OTEL_TRACES_EXPORTER|OTEL_EXPORTER_OTLP_TRACES_ENDPOINT|OTEL_EXPORTER_OTLP_TRACES_PROTOCOL|OTEL_EXPORTER_OTLP_TRACES_HEADERS|` +
	`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT|OTEL_EXPORTER_OTLP_METRICS_PROTOCOL|OTEL_EXPORTER_OTLP_METRICS_HEADERS|` +
	`OTEL_METRIC_EXPORT_INTERVAL|OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE|` +
	`OTEL_LOGS_EXPORTER|OTEL_EXPORTER_OTLP_LOGS_ENDPOINT|OTEL_EXPORTER_OTLP_LOGS_PROTOCOL|OTEL_EXPORTER_OTLP_LOGS_HEADERS|` +
	`OTEL_BLRP_SCHEDULE_DELAY|OTEL_BLRP_EXPORT_TIMEOUT|OTEL_BLRP_MAX_QUEUE_SIZE|OTEL_BLRP_MAX_EXPORT_BATCH_SIZE|` +
	`OTEL_LOG_LEVEL|OTEL_SERVICE_NAME|OTEL_RESOURCE_ATTRIBUTES|OTEL_PROPAGATORS|OTEL_TRACES_SAMPLER|OTEL_TRACES_SAMPLER_ARG)":` +
	`(?:"[^"]*"|true|false|[0-9]+|\{[^}]*\}))*`

// commonLogIgnore is a list of strings that are commonly ignored by tests
// involving the output of the logger.
var commonLogIgnore = []string{"appsec: ", "telemetry", "Runtime metrics v2 enabled"}

func TestStartupLog(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	t.Setenv("DD_METRICS_OTEL_ENABLED", "")
	t.Setenv("DD_LOGS_OTEL_ENABLED", "")

	t.Run("basic", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp))
		require.NoError(t, err)
		defer stop()

		tp.Reset()
		tp.Ignore(commonLogIgnore...)
		logStartup(tracer)
		require.Len(t, tp.Logs(), 2)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test(\.exe)?","agent_url":"http://localhost:9/v1.0/traces","agent_error":"Post .*","debug":false,"analytics_enabled":false,"sample_rate":"NaN","sample_rate_limit":"disabled","trace_sampling_rules":null,"span_sampling_rules":null,"sampling_rules_error":"","service_mappings":null,"tags":{"runtime-id":"[^"]*"},"runtime_metrics_enabled":false,"runtime_metrics_v2_enabled":true,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"","architecture":"[^"]*","global_service":"","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":true,"Stats":true,"StatsdPort":(0|8125),"AgentVersion":"[^"]*"},"integrations":{.*},"partial_flush_enabled":false,"partial_flush_min_spans":1000,"orchestrion":{"enabled":false},"feature_flags":\[\],"propagation_style_inject":"datadog,tracecontext,baggage","propagation_style_extract":"datadog,tracecontext,baggage","propagation_behavior_extract":"continue","tracing_as_transport":false,"dogstatsd_address":"localhost:8125","data_streams_enabled":false`+otelStartupFieldsRegexp+`}`, tp.Logs()[1])
	})

	t.Run("agent version surfaces", func(t *testing.T) {
		// AgentVersion is exported specifically so it lands in the startup
		// log — the field a support triager will actually want.
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp))
		require.NoError(t, err)
		defer stop()

		af := tracer.config.agent.load()
		af.AgentVersion = "7.78.2"
		tracer.config.agent.store(af)

		tp.Reset()
		tp.Ignore(commonLogIgnore...)
		logStartup(tracer)
		require.Len(t, tp.Logs(), 2)
		assert.Regexp(`"agent_features":{"DropP0s":true,"Stats":true,"StatsdPort":\d+,"AgentVersion":"7\.78\.2"}`, tp.Logs()[1])
	})

	t.Run("configured", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)

		t.Setenv("DD_TRACE_SAMPLE_RATE", "0.123")
		tracer, _, _, stop, err := startTestTracer(t,
			WithLogger(tp),
			WithService("configured.service"),
			WithAgentAddr("test.host:1234"),
			WithEnv("configuredEnv"),
			WithServiceMapping("initial_service", "new_service"),
			WithGlobalTag("tag", "value"),
			WithGlobalTag("tag2", math.NaN()),
			WithRuntimeMetrics(),
			WithAnalyticsRate(1.0),
			WithServiceVersion("2.3.4"),
			WithSamplingRules(TraceSamplingRules(Rule{ServiceGlob: "mysql", Rate: 0.75})),
			WithDebugMode(true),
			WithFeatureFlags("discovery"),
		)
		require.NoError(t, err)
		defer globalconfig.SetAnalyticsRate(math.NaN())
		defer globalconfig.SetServiceName("")
		defer stop()

		tp.Reset()
		tp.Ignore(commonLogIgnore...)
		logStartup(tracer)
		require.Len(t, tp.Logs(), 2)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"configuredEnv","service":"configured.service","agent_url":"http://localhost:9/v1.0/traces","agent_error":"Post .*","debug":true,"analytics_enabled":true,"sample_rate":"0\.123000","sample_rate_limit":"100","trace_sampling_rules":\[{"service":"mysql","sample_rate":0\.75}\],"span_sampling_rules":null,"sampling_rules_error":"","service_mappings":{"initial_service":"new_service"},"tags":{"runtime-id":"[^"]*","tag":"value","tag2":"NaN"},"runtime_metrics_enabled":true,"runtime_metrics_v2_enabled":true,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"2.3.4","architecture":"[^"]*","global_service":"configured.service","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":true,"Stats":true,"StatsdPort":(0|8125),"AgentVersion":"[^"]*"},"integrations":{.*},"partial_flush_enabled":false,"partial_flush_min_spans":1000,"orchestrion":{"enabled":(false|true,"metadata":{"version":"v\d+.\d+.\d+(-[^"]+)?"})},"feature_flags":\["discovery"\],"propagation_style_inject":"datadog,tracecontext,baggage","propagation_style_extract":"datadog,tracecontext,baggage","propagation_behavior_extract":"continue","tracing_as_transport":false,"dogstatsd_address":"localhost:8125","data_streams_enabled":false`+otelStartupFieldsRegexp+`}`, tp.Logs()[1])
	})

	t.Run("limit", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		t.Setenv("DD_TRACE_SAMPLE_RATE", "0.123")
		t.Setenv("DD_TRACE_RATE_LIMIT", "1000.001")
		tracer, _, _, stop, err := startTestTracer(t,
			WithLogger(tp),
			WithService("configured.service"),
			WithAgentAddr("test.host:1234"),
			WithEnv("configuredEnv"),
			WithServiceMapping("initial_service", "new_service"),
			WithGlobalTag("tag", "value"),
			WithGlobalTag("tag2", math.NaN()),
			WithRuntimeMetrics(),
			WithAnalyticsRate(1.0),
			WithServiceVersion("2.3.4"),
			WithSamplingRules(TraceSamplingRules(Rule{ServiceGlob: "mysql", Rate: 0.75})),
			WithDebugMode(true),
		)
		require.NoError(t, err)
		defer globalconfig.SetAnalyticsRate(math.NaN())
		defer globalconfig.SetServiceName("")
		defer stop()

		tp.Reset()
		tp.Ignore(commonLogIgnore...)
		logStartup(tracer)
		require.Len(t, tp.Logs(), 2)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"configuredEnv","service":"configured.service","agent_url":"http://localhost:9/v1.0/traces","agent_error":"Post .*","debug":true,"analytics_enabled":true,"sample_rate":"0\.123000","sample_rate_limit":"1000.001","trace_sampling_rules":\[{"service":"mysql","sample_rate":0\.75}\],"span_sampling_rules":null,"sampling_rules_error":"","service_mappings":{"initial_service":"new_service"},"tags":{"runtime-id":"[^"]*","tag":"value","tag2":"NaN"},"runtime_metrics_enabled":true,"runtime_metrics_v2_enabled":true,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"2.3.4","architecture":"[^"]*","global_service":"configured.service","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":true,"Stats":true,"StatsdPort":(0|8125),"AgentVersion":"[^"]*"},"integrations":{.*},"partial_flush_enabled":false,"partial_flush_min_spans":1000,"orchestrion":{"enabled":false},"feature_flags":\[\],"propagation_style_inject":"datadog,tracecontext,baggage","propagation_style_extract":"datadog,tracecontext,baggage","propagation_behavior_extract":"continue","tracing_as_transport":false,"dogstatsd_address":"localhost:8125","data_streams_enabled":false`+otelStartupFieldsRegexp+`}`, tp.Logs()[1])
	})

	t.Run("errors", func(t *testing.T) {
		tp := new(log.RecordLogger)
		t.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service","sample_rate": 0.234}, {"service": "other.service","sample_rate": 2}]`)
		_, _, _, stop, err := startTestTracer(t, WithLogger(tp))
		require.NoError(t, err)
		defer stop()
	})

	t.Run("lambda", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithLambdaMode(true))
		require.NoError(t, err)
		defer stop()

		tp.Reset()
		tp.Ignore(commonLogIgnore...)
		logStartup(tracer)
		assert.Len(tp.Logs(), 1)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test(\.exe)?","agent_url":"http://localhost:9/v1.0/traces","agent_error":"","debug":false,"analytics_enabled":false,"sample_rate":"NaN","sample_rate_limit":"disabled","trace_sampling_rules":null,"span_sampling_rules":null,"sampling_rules_error":"","service_mappings":null,"tags":{"runtime-id":"[^"]*"},"runtime_metrics_enabled":false,"runtime_metrics_v2_enabled":true,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"","architecture":"[^"]*","global_service":"","lambda_mode":"true","appsec":((true)|(false)),"agent_features":{"DropP0s":true,"Stats":true,"StatsdPort":(0|8125),"AgentVersion":"[^"]*"},"integrations":{.*},"partial_flush_enabled":false,"partial_flush_min_spans":1000,"orchestrion":{"enabled":false},"feature_flags":\[\],"propagation_style_inject":"datadog,tracecontext,baggage","propagation_style_extract":"datadog,tracecontext,baggage","propagation_behavior_extract":"continue","tracing_as_transport":false,"dogstatsd_address":"localhost:8125","data_streams_enabled":false`+otelStartupFieldsRegexp+`}`, tp.Logs()[0])
	})

	t.Run("integrations", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp))
		require.NoError(t, err)
		defer stop()
		tp.Reset()
		tp.Ignore(commonLogIgnore...)
		logStartup(tracer)
		require.Len(t, tp.Logs(), 2)

		for n, s := range tracer.config.integrations {
			expect := fmt.Sprintf("\"%s\":{\"instrumented\":%t,\"available\":%t,\"available_version\":\"%s\"}", n, s.Instrumented, s.Available, s.Version)
			assert.Contains(tp.Logs()[1], expect, "expected integration %s", expect)
		}
	})

}

func TestLogSamplingRules(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tp.Ignore(commonLogIgnore...)
	log.UseLogger(tp)
	t.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service", "sample_rate": 0.234}, {"service": "other.service"}, {"service": "last.service", "sample_rate": 0.56}, {"odd": "pairs"}, {"sample_rate": 9.10}]`)
	_, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithEnv("test"))
	require.NoError(t, err)
	defer stop()

	assert.Len(tp.Logs(), 1)
	assert.Regexp(logPrefixRegexp+` WARN: DIAGNOSTICS Error\(s\) parsing DD_TRACE_SAMPLING_RULES: at index 4: ignoring rule \{Rate:9\.10\}: rate is out of \[0\.0, 1\.0\] range$`, tp.Logs()[0])
}

func TestLogDefaultSampleRate(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tp.Ignore(commonLogIgnore...)
	log.UseLogger(tp)
	t.Setenv("DD_TRACE_SAMPLE_RATE", ``)
	_, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithEnv("test"))
	require.NoError(t, err)
	defer stop()

	assert.Len(tp.Logs(), 0)
}

func TestLogAgentReachable(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp))
	require.NoError(t, err)
	defer stop()
	tp.Reset()
	tp.Ignore(commonLogIgnore...)
	logStartup(tracer)
	require.Len(t, tp.Logs(), 2)
	assert.Regexp(logPrefixRegexp+` WARN: DIAGNOSTICS Unable to reach agent intake: Post`, tp.Logs()[0])
}

func TestLogFormat(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)

	tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithRuntimeMetrics(), WithDebugMode(true))
	require.NoError(t, err)
	defer stop()
	tp.Reset()
	tp.Ignore(commonLogIgnore...)
	span := tracer.StartSpan("test", ServiceName("test-service"), ResourceName("/"), WithSpanID(12345))
	assert.Len(tp.Logs(), 1)
	assert.Regexp(logPrefixRegexp+` DEBUG: Started Span: dd.trace_id="`+span.Context().TraceID()+`" dd.span_id="12345" dd.parent_id="0", Operation: test, Resource: /, Tags: map.*, map.*`, tp.Logs()[0])
}

func TestLogPropagators(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		substring := `"propagation_style_inject":"datadog,tracecontext,baggage","propagation_style_extract":"datadog,tracecontext,baggage"`
		log := setup(t, nil)
		assert.Regexp(substring, log)
	})
	t.Run("datadog,tracecontext", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_PROPAGATION_STYLE", "datadog,tracecontext")
		substring := `"propagation_style_inject":"datadog,tracecontext","propagation_style_extract":"datadog,tracecontext"`
		log := setup(t, nil)
		assert.Regexp(substring, log)
	})
	t.Run("b3multi", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_PROPAGATION_STYLE", "b3multi")
		substring := `"propagation_style_inject":"b3multi","propagation_style_extract":"b3multi"`
		log := setup(t, nil)
		assert.Regexp(substring, log)
	})
	t.Run("none", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_PROPAGATION_STYLE", "none")
		substring := `"propagation_style_inject":"","propagation_style_extract":""`
		log := setup(t, nil)
		assert.Regexp(substring, log)
	})
	t.Run("different-injector-extractor", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_PROPAGATION_STYLE_INJECT", "b3multi")
		t.Setenv("DD_TRACE_PROPAGATION_STYLE_EXTRACT", "tracecontext")
		substring := `"propagation_style_inject":"b3multi","propagation_style_extract":"tracecontext"`
		log := setup(t, nil)
		assert.Regexp(substring, log)
	})
	t.Run("otel-propagators", func(t *testing.T) {
		t.Setenv("DD_TRACE_PROPAGATION_STYLE", "tracecontext")
		log := setup(t, nil)
		assert.Contains(t, log, `"OTEL_PROPAGATORS":"tracecontext"`)
	})
	t.Run("otel-propagators-extract-ignored", func(t *testing.T) {
		t.Setenv("DD_TRACE_PROPAGATION_STYLE", "tracecontext")
		t.Setenv("DD_TRACE_PROPAGATION_BEHAVIOR_EXTRACT", "ignore")
		log := setup(t, nil)
		assert.NotContains(t, log, `"OTEL_PROPAGATORS"`)
	})
	t.Run("custom-propagator", func(t *testing.T) {
		assert := assert.New(t)
		substring := `"propagation_style_inject":"custom","propagation_style_extract":"custom"`
		p := &prop{}
		log := setup(t, p)
		assert.Regexp(substring, log)
	})
}

func TestOTelPropagators(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Propagator
		want string
	}{
		{"b3", NewPropagator(&PropagatorConfig{InjectStyle: "b3", ExtractStyle: "b3"}), "b3multi"},
		{"b3multi", NewPropagator(&PropagatorConfig{InjectStyle: "b3multi", ExtractStyle: "b3multi"}), "b3multi"},
		{"b3 single header", NewPropagator(&PropagatorConfig{InjectStyle: "b3 single header", ExtractStyle: "b3 single header"}), "b3"},
		{"default", NewPropagator(nil), "datadog,tracecontext,baggage"},
		{"none", NewPropagator(&PropagatorConfig{InjectStyle: "none", ExtractStyle: "none"}), "none"},
		{"different", NewPropagator(&PropagatorConfig{InjectStyle: "b3multi", ExtractStyle: "tracecontext"}), ""},
		{"extract ignored", NewPropagator(&PropagatorConfig{BehaviorExtract: propagationBehaviorExtractIgnore}), ""},
		{"explicit custom", NewPropagator(nil, &prop{}), ""},
		{"custom", &prop{}, ""},
	} {
		assert.Equal(t, tc.want, otelPropagators(tc.p), tc.name)
	}
}

func TestCommonOTLPExporterInfo(t *testing.T) {
	a := otlpExporterInfo{"http://agent:4318/v1/metrics", "http/protobuf", map[string]string{"api-key": "a", "tenant": "t"}}
	b := otlpExporterInfo{"http://agent:4317", "grpc", map[string]string{"api-key": "b"}}
	redacted := map[string]string{"api-key": redactedHeaderValue, "tenant": redactedHeaderValue}
	for _, tc := range []struct {
		name               string
		exporters          []otlpExporterInfo
		endpoint, protocol string
		headers            map[string]string
	}{
		{"none", nil, "", "", nil},
		{"no headers", []otlpExporterInfo{{endpoint: a.endpoint}, {endpoint: a.endpoint}}, a.endpoint, "", map[string]string{}},
		{"one", []otlpExporterInfo{a}, a.endpoint, a.protocol, redacted},
		{"same", []otlpExporterInfo{a, a}, a.endpoint, a.protocol, redacted},
		{"different protocol", []otlpExporterInfo{a, {a.endpoint, "http/json", a.headers}}, a.endpoint, conflictingOTLPValue, redacted},
		{"headers on one exporter", []otlpExporterInfo{{a.endpoint, a.protocol, nil}, a}, a.endpoint, a.protocol, map[string]string{"api-key": conflictingOTLPValue, "tenant": conflictingOTLPValue}},
		// Header values are compared before redaction.
		{"different", []otlpExporterInfo{a, b}, conflictingOTLPValue, conflictingOTLPValue, map[string]string{"api-key": conflictingOTLPValue, "tenant": conflictingOTLPValue}},
		{"third exporter differs", []otlpExporterInfo{a, a, {a.endpoint, a.protocol, map[string]string{"api-key": "a"}}}, a.endpoint, a.protocol, map[string]string{"api-key": redactedHeaderValue, "tenant": conflictingOTLPValue}},
	} {
		endpoint, protocol, headers := commonOTLPExporterInfo(tc.exporters)
		assert.Equal(t, tc.endpoint, endpoint, tc.name)
		assert.Equal(t, tc.protocol, protocol, tc.name)
		assert.Equal(t, tc.headers, headers, tc.name)
	}
}

func TestOTelStartupInfoMetricsAndLogs(t *testing.T) {
	t.Setenv("DD_AGENT_HOST", "agent")
	// The MeterProvider exports over gRPC; span metrics only support HTTP and fall back to http/protobuf.
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL", "grpc")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_HEADERS", "api-key=secret")
	t.Setenv("DD_LOGS_OTEL_ENABLED", "true")
	t.Setenv("OTEL_BLRP_SCHEDULE_DELAY", "2000")
	t.Setenv("OTEL_BLRP_EXPORT_TIMEOUT", "7000")
	t.Setenv("OTEL_BLRP_MAX_QUEUE_SIZE", "100")
	t.Setenv("OTEL_BLRP_MAX_EXPORT_BATCH_SIZE", "500")
	tr, err := newUnstartedTracer(func(c *config) {
		c.internalConfig.SetOTLPSpanMetricsEnabled(true, internalconfig.OriginCode)
	})
	require.NoError(t, err)
	defer tr.Stop()
	tr.config.otelRuntimeMetricsShouldBeEnabled = true

	var info startupInfo
	tr.addOTelStartupInfo(&info)
	assert.True(t, info.OTelEnabled)
	assert.Equal(t, conflictingOTLPValue, info.OTLPMetricsEndpoint)
	assert.Equal(t, conflictingOTLPValue, info.OTLPMetricsProtocol)
	assert.Equal(t, map[string]string{"api-key": redactedHeaderValue}, info.OTLPMetricsHeaders)
	assert.Equal(t, "otlp", info.OTelLogsExporter)
	assert.Equal(t, "http://agent:4318/v1/logs", info.OTLPLogsEndpoint)
	assert.Equal(t, "http/protobuf", info.OTLPLogsProtocol)
	assert.Equal(t, int64(2000), info.BLRPScheduleDelay)
	assert.Equal(t, int64(7000), info.BLRPExportTimeout)
	assert.Equal(t, 100, info.BLRPMaxQueueSize)
	assert.Equal(t, 100, info.BLRPMaxExportBatchSize)
}

func TestOTLPLogsEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name               string
		env                map[string]string
		endpoint, protocol string
	}{
		{"agent", map[string]string{"DD_AGENT_HOST": "agent"}, "http://agent:4318/v1/logs", "http/protobuf"},
		{"agent grpc", map[string]string{"DD_AGENT_HOST": "agent", "OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"}, "http://agent:4317", "grpc"},
		{"generic", map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4318/"}, "http://collector:4318/v1/logs", "http/protobuf"},
		{"signal", map[string]string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "https://u:p@collector/v1/logs?k=v"}, "https://collector/v1/logs", "http/protobuf"},
		{"signal encoded path", map[string]string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://collector:4318/tenant%2Fblue"}, "http://collector:4318/tenant/blue", "http/protobuf"},
		{"signal grpc", map[string]string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "grpc://collector:4317", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "grpc"}, "http://collector:4317", "grpc"},
		{"signal grpc https", map[string]string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "https://collector:4317", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "grpc"}, "https://collector:4317", "grpc"},
		{"signal grpc other scheme", map[string]string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "dns://collector:4317", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "grpc"}, "http://collector:4317", "grpc"},
		{"signal grpc no host", map[string]string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "collector:4317", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "grpc"}, "", "grpc"},
		{"signal grpc no scheme", map[string]string{"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "//collector:4317", "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "grpc"}, "https://collector:4317", "grpc"},
		{"https agent", map[string]string{"DD_TRACE_AGENT_URL": "https://agent:8126"}, "https://agent:4318/v1/logs", "http/protobuf"},
		{"unix agent", map[string]string{"DD_TRACE_AGENT_URL": "unix:///var/run/datadog/apm.socket"}, "http://localhost:4318/v1/logs", "http/protobuf"},
		{"unix agent with host", map[string]string{"DD_TRACE_AGENT_URL": "unix:///var/run/datadog/apm.socket", "DD_AGENT_HOST": "agent"}, "http://agent:4318/v1/logs", "http/protobuf"},
		{"unix agent grpc", map[string]string{"DD_TRACE_AGENT_URL": "unix:///var/run/datadog/apm.socket", "OTEL_EXPORTER_OTLP_PROTOCOL": "grpc"}, "http://localhost:4317", "grpc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			endpoint, protocol := otlpLogsEndpoint(internalconfig.CreateNew())
			assert.Equal(t, tc.endpoint, endpoint)
			assert.Equal(t, tc.protocol, protocol)
		})
	}
}

type prop struct{}

func (p *prop) Inject(_ *SpanContext, _ any) (e error) {
	return
}
func (p *prop) Extract(_ any) (sctx *SpanContext, e error) {
	return
}

func setup(t *testing.T, customProp Propagator) string {
	tp := new(log.RecordLogger)
	var tracer *tracer
	var stop func()
	var err error
	if customProp != nil {
		tracer, _, _, stop, err = startTestTracer(t, WithLogger(tp), WithPropagator(customProp))
		assert.NoError(t, err)
	} else {
		tracer, _, _, stop, err = startTestTracer(t, WithLogger(tp))
		assert.NoError(t, err)
	}
	defer stop()
	tp.Reset()
	tp.Ignore(commonLogIgnore...)
	logStartup(tracer)
	require.Len(t, tp.Logs(), 2)
	return tp.Logs()[1]
}

func findLogEntry(logs []string, pattern string) (string, bool) {
	for _, log := range logs {
		if matched, _ := regexp.MatchString(pattern, log); matched {
			return log, true
		}
	}
	return "", false
}

func TestAgentURL(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tracer, err := newTracer(WithLogger(tp), WithUDS("/var/run/datadog/apm.socket"))
	assert.Nil(err)
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore(commonLogIgnore...)
	logStartup(tracer)
	logEntry, found := findLogEntry(tp.Logs(), `"agent_url":"unix:///var/run/datadog/apm.socket"`)
	if !found {
		t.Fatal("Expected to find log entry")
	}
	assert.Regexp(`"agent_url":"unix:///var/run/datadog/apm.socket"`, logEntry)
}

func TestAgentURLFromEnv(t *testing.T) {
	assert := assert.New(t)
	t.Setenv("DD_TRACE_AGENT_URL", "unix:///var/run/datadog/apm.socket")
	tp := new(log.RecordLogger)
	tracer, err := newTracer(WithLogger(tp))
	assert.Nil(err)
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore(commonLogIgnore...)
	logStartup(tracer)
	logEntry, found := findLogEntry(tp.Logs(), `"agent_url":"unix:///var/run/datadog/apm.socket"`)
	if !found {
		t.Fatal("Expected to find log entry")
	}
	assert.Regexp(`"agent_url":"unix:///var/run/datadog/apm.socket"`, logEntry)
}

func TestInvalidAgentURL(t *testing.T) {
	assert := assert.New(t)
	// invalid socket URL
	t.Setenv("DD_TRACE_AGENT_URL", "var/run/datadog/apm.socket")
	tp := new(log.RecordLogger)
	tracer, err := newTracer(WithLogger(tp), withNoopInfoHTTPClient())
	assert.Nil(err)
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore(commonLogIgnore...)
	logStartup(tracer)
	logEntry, found := findLogEntry(tp.Logs(), `"agent_url":"http://localhost:8126/v0.4/traces"`) // falls back to v0.4 when agent doesn't support v1
	if !found {
		t.Fatal("Expected to find log entry")
	}
	// assert that it is the default URL
	assert.Regexp(`"agent_url":"http://localhost:8126/v0.4/traces"`, logEntry)
}

func TestAgentURLConflict(t *testing.T) {
	assert := assert.New(t)
	t.Setenv("DD_TRACE_AGENT_URL", "unix:///var/run/datadog/apm.socket")
	tp := new(log.RecordLogger)
	tracer, err := newTracer(WithLogger(tp), WithUDS("/var/run/datadog/apm.socket"), WithAgentAddr("localhost:8126"), withNoopInfoHTTPClient())
	assert.Nil(err)
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore(commonLogIgnore...)
	logStartup(tracer)
	logEntry, found := findLogEntry(tp.Logs(), `"agent_url":"http://localhost:8126/v0.4/traces"`) // falls back to v0.4 when agent doesn't support v1
	if !found {
		t.Fatal("Expected to find log entry")
	}
	assert.Regexp(`"agent_url":"http://localhost:8126/v0.4/traces"`, logEntry)
}

func TestRedactOTLPHeaders(t *testing.T) {
	headers := map[string]string{"dd-api-key": "secret", "Authorization": "Bearer token"}
	assert.Equal(t, map[string]string{"dd-api-key": "REDACTED", "Authorization": "REDACTED"}, redactOTLPHeaders(headers))
	assert.Equal(t, "secret", headers["dd-api-key"])
	assert.Equal(t, map[string]string{}, redactOTLPHeaders(nil))
}

func TestOTelTracesSampler(t *testing.T) {
	for _, tc := range []struct {
		name     string
		env      map[string]string
		opts     []StartOption
		wantName string
		wantArg  string
	}{
		{name: "custom sampler", opts: []StartOption{WithSampler(NewAllSampler())}, wantName: "datadog_custom_sampler"},
		{name: "sampler rate below 1", opts: []StartOption{WithSamplerRate(0.5)}, wantName: "datadog_sampler_rate"},
		{name: "global rate 0", env: map[string]string{"DD_TRACE_SAMPLE_RATE": "0"}, wantName: "parentbased_always_off"},
		{name: "global rate 1", env: map[string]string{"DD_TRACE_SAMPLE_RATE": "1"}, wantName: "parentbased_always_on"},
		{name: "global rate between", env: map[string]string{"DD_TRACE_SAMPLE_RATE": "0.25"}, wantName: "parentbased_traceidratio", wantArg: "0.25"},
		{
			name:     "rules",
			env:      map[string]string{"DD_TRACE_SAMPLE_RATE": "0.25", "DD_TRACE_SAMPLING_RULES": `[{"service": "some.service", "sample_rate": 0.5}]`},
			wantName: "datadog_custom_rules",
		},
		{name: "otlp default", env: map[string]string{"OTEL_TRACES_EXPORTER": "otlp"}, wantName: "parentbased_always_on"},
		{name: "agent rates", wantName: "datadog_agent_rates"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			tr, err := newUnstartedTracer(append(tc.opts, withNoopInfoHTTPClient())...)
			require.NoError(t, err)
			defer tr.Stop()
			name, arg := tr.otelTracesSampler()
			assert.Equal(t, tc.wantName, name)
			assert.Equal(t, tc.wantArg, arg)
		})
	}
}

func TestSanitizeOTLPEndpoint(t *testing.T) {
	for _, tc := range []struct{ endpoint, want string }{
		{"https://user:password@collector:4318/v1/traces?token=x#frag", "https://collector:4318/v1/traces"},
		{"https:user:password@collector/v1/traces", ""},
		{"/v1/traces", ""},
		{"://invalid", ""},
	} {
		assert.Equal(t, tc.want, sanitizeOTLPEndpoint(tc.endpoint), tc.endpoint)
	}
}
