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

	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const logPrefixRegexp = `Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+(-((rc|beta)\.[0-9]+|dev(\.\d+)?))?`

func TestStartupLog(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp))
		require.NoError(t, err)
		defer stop()

		tp.Reset()
		tp.Ignore("appsec: ", "telemetry")
		logStartup(tracer)
		require.Len(t, tp.Logs(), 2)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test(\.exe)?","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":false,"analytics_enabled":false,"sample_rate":"NaN","sample_rate_limit":"disabled","trace_sampling_rules":null,"span_sampling_rules":null,"sampling_rules_error":"","service_mappings":null,"tags":{"runtime-id":"[^"]*"},"runtime_metrics_enabled":false,"runtime_metrics_v2_enabled":false,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"","architecture":"[^"]*","global_service":"","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":true,"Stats":true,"StatsdPort":(0|8125)},"integrations":{.*},"partial_flush_enabled":false,"partial_flush_min_spans":1000,"orchestrion":{"enabled":false},"feature_flags":\[\],"propagation_style_inject":"datadog,tracecontext,baggage","propagation_style_extract":"datadog,tracecontext,baggage","tracing_as_transport":false,"dogstatsd_address":"localhost:8125","data_streams_enabled":false}`, tp.Logs()[1])
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
		tp.Ignore("appsec: ", "telemetry")
		logStartup(tracer)
		require.Len(t, tp.Logs(), 2)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"configuredEnv","service":"configured.service","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":true,"analytics_enabled":true,"sample_rate":"0\.123000","sample_rate_limit":"100","trace_sampling_rules":\[{"service":"mysql","sample_rate":0\.75}\],"span_sampling_rules":null,"sampling_rules_error":"","service_mappings":{"initial_service":"new_service"},"tags":{"runtime-id":"[^"]*","tag":"value","tag2":"NaN"},"runtime_metrics_enabled":true,"runtime_metrics_v2_enabled":false,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"2.3.4","architecture":"[^"]*","global_service":"configured.service","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":true,"Stats":true,"StatsdPort":(0|8125)},"integrations":{.*},"partial_flush_enabled":false,"partial_flush_min_spans":1000,"orchestrion":{"enabled":(false|true,"metadata":{"version":"v\d+.\d+.\d+(-[^"]+)?"})},"feature_flags":\["discovery"\],"propagation_style_inject":"datadog,tracecontext,baggage","propagation_style_extract":"datadog,tracecontext,baggage","tracing_as_transport":false,"dogstatsd_address":"localhost:8125","data_streams_enabled":false}`, tp.Logs()[1])
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
		tp.Ignore("appsec: ", "telemetry")
		logStartup(tracer)
		require.Len(t, tp.Logs(), 2)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"configuredEnv","service":"configured.service","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":true,"analytics_enabled":true,"sample_rate":"0\.123000","sample_rate_limit":"1000.001","trace_sampling_rules":\[{"service":"mysql","sample_rate":0\.75}\],"span_sampling_rules":null,"sampling_rules_error":"","service_mappings":{"initial_service":"new_service"},"tags":{"runtime-id":"[^"]*","tag":"value","tag2":"NaN"},"runtime_metrics_enabled":true,"runtime_metrics_v2_enabled":false,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"2.3.4","architecture":"[^"]*","global_service":"configured.service","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":true,"Stats":true,"StatsdPort":(0|8125)},"integrations":{.*},"partial_flush_enabled":false,"partial_flush_min_spans":1000,"orchestrion":{"enabled":false},"feature_flags":\[\],"propagation_style_inject":"datadog,tracecontext,baggage","propagation_style_extract":"datadog,tracecontext,baggage","tracing_as_transport":false,"dogstatsd_address":"localhost:8125","data_streams_enabled":false}`, tp.Logs()[1])
	})

	t.Run("errors", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		t.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service","sample_rate": 0.234}, {"service": "other.service","sample_rate": 2}]`)
		_, _, _, _, err := startTestTracer(t, WithLogger(tp))
		assert.Equal("found errors when parsing sampling rules: \n\tat index 1: ignoring rule {Service:other.service Rate:2}: rate is out of [0.0, 1.0] range", err.Error())
	})

	t.Run("lambda", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithLambdaMode(true))
		require.NoError(t, err)
		defer stop()

		tp.Reset()
		tp.Ignore("appsec: ", "telemetry")
		logStartup(tracer)
		assert.Len(tp.Logs(), 1)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test(\.exe)?","agent_url":"http://localhost:9/v0.4/traces","agent_error":"","debug":false,"analytics_enabled":false,"sample_rate":"NaN","sample_rate_limit":"disabled","trace_sampling_rules":null,"span_sampling_rules":null,"sampling_rules_error":"","service_mappings":null,"tags":{"runtime-id":"[^"]*"},"runtime_metrics_enabled":false,"runtime_metrics_v2_enabled":false,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"","architecture":"[^"]*","global_service":"","lambda_mode":"true","appsec":((true)|(false)),"agent_features":{"DropP0s":true,"Stats":true,"StatsdPort":(0|8125)},"integrations":{.*},"partial_flush_enabled":false,"partial_flush_min_spans":1000,"orchestrion":{"enabled":false},"feature_flags":\[\],"propagation_style_inject":"datadog,tracecontext,baggage","propagation_style_extract":"datadog,tracecontext,baggage","tracing_as_transport":false,"dogstatsd_address":"localhost:8125","data_streams_enabled":false}`, tp.Logs()[0])
	})

	t.Run("integrations", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		tracer, _, _, stop, err := startTestTracer(t, WithLogger(tp))
		require.NoError(t, err)
		defer stop()
		tp.Reset()
		tp.Ignore("appsec: ", "telemetry")
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
	tp.Ignore("appsec: ", "telemetry")
	t.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service", "sample_rate": 0.234}, {"service": "other.service"}, {"service": "last.service", "sample_rate": 0.56}, {"odd": "pairs"}, {"sample_rate": 9.10}]`)
	_, _, _, _, err := startTestTracer(t, WithLogger(tp), WithEnv("test"))
	assert.Error(err)

	assert.Len(tp.Logs(), 1)
	assert.Regexp(logPrefixRegexp+` WARN: DIAGNOSTICS Error\(s\) parsing sampling rules: found errors:\n\tat index 4: ignoring rule {Rate:9\.10}: rate is out of \[0\.0, 1\.0] range$`, tp.Logs()[0])
}

func TestLogDefaultSampleRate(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tp.Ignore("appsec: ", "telemetry")
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
	tp.Ignore("appsec: ", "telemetry")
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
	tp.Ignore("appsec: ", "telemetry")
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
	t.Run("custom-propagator", func(t *testing.T) {
		assert := assert.New(t)
		substring := `"propagation_style_inject":"custom","propagation_style_extract":"custom"`
		p := &prop{}
		log := setup(t, p)
		assert.Regexp(substring, log)
	})
}

type prop struct{}

func (p *prop) Inject(_ *SpanContext, _ interface{}) (e error) {
	return
}
func (p *prop) Extract(_ interface{}) (sctx *SpanContext, e error) {
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
	tp.Ignore("appsec: ", "telemetry")
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
	tracer, err := newTracer(WithLogger(tp), WithUDS("var/run/datadog/apm.socket"))
	assert.Nil(err)
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore("appsec: ", "telemetry")
	logStartup(tracer)
	logEntry, found := findLogEntry(tp.Logs(), `"agent_url":"unix://var/run/datadog/apm.socket"`)
	if !found {
		t.Fatal("Expected to find log entry")
	}
	assert.Regexp(`"agent_url":"unix://var/run/datadog/apm.socket"`, logEntry)
}

func TestAgentURLFromEnv(t *testing.T) {
	assert := assert.New(t)
	t.Setenv("DD_TRACE_AGENT_URL", "unix://var/run/datadog/apm.socket")
	tp := new(log.RecordLogger)
	tracer, err := newTracer(WithLogger(tp))
	assert.Nil(err)
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore("appsec: ", "telemetry")
	logStartup(tracer)
	logEntry, found := findLogEntry(tp.Logs(), `"agent_url":"unix://var/run/datadog/apm.socket"`)
	if !found {
		t.Fatal("Expected to find log entry")
	}
	assert.Regexp(`"agent_url":"unix://var/run/datadog/apm.socket"`, logEntry)
}

func TestInvalidAgentURL(t *testing.T) {
	assert := assert.New(t)
	// invalid socket URL
	t.Setenv("DD_TRACE_AGENT_URL", "var/run/datadog/apm.socket")
	tp := new(log.RecordLogger)
	tracer, err := newTracer(WithLogger(tp))
	assert.Nil(err)
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore("appsec: ", "telemetry")
	logStartup(tracer)
	logEntry, found := findLogEntry(tp.Logs(), `"agent_url":"http://localhost:8126/v0.4/traces"`)
	if !found {
		t.Fatal("Expected to find log entry")
	}
	// assert that it is the default URL
	assert.Regexp(`"agent_url":"http://localhost:8126/v0.4/traces"`, logEntry)
}

func TestAgentURLConflict(t *testing.T) {
	assert := assert.New(t)
	t.Setenv("DD_TRACE_AGENT_URL", "unix://var/run/datadog/apm.socket")
	tp := new(log.RecordLogger)
	tracer, err := newTracer(WithLogger(tp), WithUDS("var/run/datadog/apm.socket"), WithAgentAddr("localhost:8126"))
	assert.Nil(err)
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore("appsec: ", "telemetry")
	logStartup(tracer)
	logEntry, found := findLogEntry(tp.Logs(), `"agent_url":"http://localhost:8126/v0.4/traces"`)
	if !found {
		t.Fatal("Expected to find log entry")
	}
	assert.Regexp(`"agent_url":"http://localhost:8126/v0.4/traces"`, logEntry)
}
