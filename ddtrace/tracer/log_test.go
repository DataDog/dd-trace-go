// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"math"
	"os"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const logPrefixRegexp = `Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+(-(rc\.[0-9]+|dev))?`

func TestStartupLog(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
		defer stop()

		tp.Reset()
		tp.Ignore("appsec: ", telemetry.LogPrefix)
		logStartup(tracer)
		require.Len(t, tp.Logs(), 2)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test(\.exe)?","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":false,"analytics_enabled":false,"sample_rate":"NaN","sample_rate_limit":"disabled","sampling_rules":null,"sampling_rules_error":"","service_mappings":null,"tags":{"runtime-id":"[^"]*"},"runtime_metrics_enabled":false,"health_metrics_enabled":false,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"","architecture":"[^"]*","global_service":"","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":((true)|(false)),"Stats":((true)|(false)),"StatsdPort":0}}`, tp.Logs()[1])
	})

	t.Run("configured", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)

		os.Setenv("DD_TRACE_SAMPLE_RATE", "0.123")
		defer os.Unsetenv("DD_TRACE_SAMPLE_RATE")
		tracer, _, _, stop := startTestTracer(t,
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
			WithSamplingRules([]SamplingRule{ServiceRule("mysql", 0.75)}),
			WithDebugMode(true),
		)
		defer globalconfig.SetAnalyticsRate(math.NaN())
		defer globalconfig.SetServiceName("")
		defer stop()

		tp.Reset()
		tp.Ignore("appsec: ", telemetry.LogPrefix)
		logStartup(tracer)
		require.Len(t, tp.Logs(), 2)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"configuredEnv","service":"configured.service","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":true,"analytics_enabled":true,"sample_rate":"0\.123000","sample_rate_limit":"100","sampling_rules":\[{"service":"mysql","name":"","sample_rate":0\.75,"type":"trace\(0\)"}\],"sampling_rules_error":"","service_mappings":{"initial_service":"new_service"},"tags":{"runtime-id":"[^"]*","tag":"value","tag2":"NaN"},"runtime_metrics_enabled":true,"health_metrics_enabled":true,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"2.3.4","architecture":"[^"]*","global_service":"configured.service","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":false,"Stats":false,"StatsdPort":0}}`, tp.Logs()[1])
	})

	t.Run("limit", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		os.Setenv("DD_TRACE_SAMPLE_RATE", "0.123")
		defer os.Unsetenv("DD_TRACE_SAMPLE_RATE")
		os.Setenv("DD_TRACE_RATE_LIMIT", "1000.001")
		defer os.Unsetenv("DD_TRACE_RATE_LIMIT")
		tracer, _, _, stop := startTestTracer(t,
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
			WithSamplingRules([]SamplingRule{ServiceRule("mysql", 0.75)}),
			WithDebugMode(true),
		)
		defer globalconfig.SetAnalyticsRate(math.NaN())
		defer globalconfig.SetServiceName("")
		defer stop()

		tp.Reset()
		tp.Ignore("appsec: ", telemetry.LogPrefix)
		logStartup(tracer)
		require.Len(t, tp.Logs(), 2)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"configuredEnv","service":"configured.service","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":true,"analytics_enabled":true,"sample_rate":"0\.123000","sample_rate_limit":"1000.001","sampling_rules":\[{"service":"mysql","name":"","sample_rate":0\.75,"type":"trace\(0\)"}\],"sampling_rules_error":"","service_mappings":{"initial_service":"new_service"},"tags":{"runtime-id":"[^"]*","tag":"value","tag2":"NaN"},"runtime_metrics_enabled":true,"health_metrics_enabled":true,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"2.3.4","architecture":"[^"]*","global_service":"configured.service","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":false,"Stats":false,"StatsdPort":0}}`, tp.Logs()[1])
	})

	t.Run("errors", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		os.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service", "sample_rate": 0.234}, {"service": "other.service"}]`)
		defer os.Unsetenv("DD_TRACE_SAMPLING_RULES")
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
		defer stop()

		tp.Reset()
		tp.Ignore("appsec: ", telemetry.LogPrefix)
		logStartup(tracer)
		require.Len(t, tp.Logs(), 2)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test(\.exe)?","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":false,"analytics_enabled":false,"sample_rate":"NaN","sample_rate_limit":"100","sampling_rules":\[{"service":"some.service","name":"","sample_rate":0\.234,"type":"trace\(0\)"}\],"sampling_rules_error":"\\n\\tat index 1: rate not provided","service_mappings":null,"tags":{"runtime-id":"[^"]*"},"runtime_metrics_enabled":false,"health_metrics_enabled":false,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"","architecture":"[^"]*","global_service":"","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":((true)|(false)),"Stats":((true)|(false)),"StatsdPort":0}}`, tp.Logs()[1])
	})

	t.Run("lambda", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(log.RecordLogger)
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithLambdaMode(true))
		defer stop()

		tp.Reset()
		tp.Ignore("appsec: ", telemetry.LogPrefix)
		logStartup(tracer)
		assert.Len(tp.Logs(), 1)
		assert.Regexp(logPrefixRegexp+` INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test(\.exe)?","agent_url":"http://localhost:9/v0.4/traces","agent_error":"","debug":false,"analytics_enabled":false,"sample_rate":"NaN","sample_rate_limit":"disabled","sampling_rules":null,"sampling_rules_error":"","service_mappings":null,"tags":{"runtime-id":"[^"]*"},"runtime_metrics_enabled":false,"health_metrics_enabled":false,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"","architecture":"[^"]*","global_service":"","lambda_mode":"true","appsec":((true)|(false)),"agent_features":{"DropP0s":false,"Stats":false,"StatsdPort":0}}`, tp.Logs()[0])
	})
}

func TestLogSamplingRules(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tp.Ignore("appsec: ", telemetry.LogPrefix)
	os.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service", "sample_rate": 0.234}, {"service": "other.service"}, {"service": "last.service", "sample_rate": 0.56}, {"odd": "pairs"}, {"sample_rate": 9.10}]`)
	defer os.Unsetenv("DD_TRACE_SAMPLING_RULES")
	_, _, _, stop := startTestTracer(t, WithLogger(tp))
	defer stop()

	assert.Len(tp.Logs(), 1)
	assert.Regexp(logPrefixRegexp+` WARN: DIAGNOSTICS Error\(s\) parsing sampling rules: found errors:\n\tat index 1: rate not provided\n\tat index 3: rate not provided\n\tat index 4: ignoring rule {Service: Name: Rate:9\.10 MaxPerSecond:0}: rate is out of \[0\.0, 1\.0] range$`, tp.Logs()[0])
}

func TestLogAgentReachable(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
	defer stop()
	tp.Reset()
	tp.Ignore("appsec: ", telemetry.LogPrefix)
	logStartup(tracer)
	require.Len(t, tp.Logs(), 2)
	assert.Regexp(logPrefixRegexp+` WARN: DIAGNOSTICS Unable to reach agent intake: Post`, tp.Logs()[0])
}

func TestLogFormat(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tracer := newTracer(WithLogger(tp), WithRuntimeMetrics(), WithDebugMode(true))
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore("appsec: ", telemetry.LogPrefix)
	tracer.StartSpan("test", ServiceName("test-service"), ResourceName("/"), WithSpanID(12345))
	assert.Len(tp.Logs(), 1)
	assert.Regexp(logPrefixRegexp+` DEBUG: Started Span: dd.trace_id="12345" dd.span_id="12345" dd.parent_id="0", Operation: test, Resource: /, Tags: map.*, map.*`, tp.Logs()[0])
}
