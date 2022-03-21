// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"math"
	"os"
	"sync"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
)

func TestStartupLog(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(testLogger)
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
		defer stop()

		tp.Reset()
		logStartup(tracer)
		lines := removeAppSec(tp.Lines())
		assert.Len(lines, 2)
		assert.Regexp(`Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+ INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":false,"analytics_enabled":false,"sample_rate":"NaN","sampling_rules":null,"sampling_rules_error":"","service_mappings":null,"tags":{"runtime-id":"[^"]*"},"runtime_metrics_enabled":false,"health_metrics_enabled":false,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"","architecture":"[^"]*","global_service":"","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":false,"Stats":false,"StatsdPort":0,"Info":false},"long_running_enabled":((true)|(false))}`, lines[1])
	})

	t.Run("configured", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(testLogger)
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

		// Override longrunner var to get around not having an `/info` endpoint
		tracer.longrunner = &longrunner{
			stopFunc: sync.Once{},
			done:     make(chan struct{}),
		}
		go func() {
			<-tracer.longrunner.done
		}() //So we can stop the fake longrunner

		tp.Reset()
		logStartup(tracer)
		assert.Len(tp.Lines(), 2)
		assert.Regexp(`Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+ INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"configuredEnv","service":"configured.service","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":true,"analytics_enabled":true,"sample_rate":"0\.123000","sampling_rules":\[{"service":"mysql","name":"","sample_rate":0\.75}\],"sampling_rules_error":"","service_mappings":{"initial_service":"new_service"},"tags":{"runtime-id":"[^"]*","tag":"value","tag2":"NaN"},"runtime_metrics_enabled":true,"health_metrics_enabled":true,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"2.3.4","architecture":"[^"]*","global_service":"configured.service","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":false,"Stats":false,"StatsdPort":0,"Info":false},"long_running_enabled":true}`, tp.Lines()[1])
	})

	t.Run("errors", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(testLogger)
		os.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service", "sample_rate": 0.234}, {"service": "other.service"}]`)
		defer os.Unsetenv("DD_TRACE_SAMPLING_RULES")
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
		defer stop()

		tp.Reset()
		logStartup(tracer)
		assert.Len(tp.Lines(), 2)
		assert.Regexp(`Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+ INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":false,"analytics_enabled":false,"sample_rate":"NaN","sampling_rules":\[{"service":"some.service","name":"","sample_rate":0\.234}\],"sampling_rules_error":"found errors:\\n\\tat index 1: rate not provided","service_mappings":null,"tags":{"runtime-id":"[^"]*"},"runtime_metrics_enabled":false,"health_metrics_enabled":false,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"","architecture":"[^"]*","global_service":"","lambda_mode":"false","appsec":((true)|(false)),"agent_features":{"DropP0s":false,"Stats":false,"StatsdPort":0,"Info":false},"long_running_enabled":((true)|(false))}`, tp.Lines()[1])
	})

	t.Run("lambda", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(testLogger)
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp), WithLambdaMode(true))
		defer stop()

		tp.Reset()
		logStartup(tracer)
		assert.Len(tp.Lines(), 1)
		assert.Regexp(`Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+ INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test","agent_url":"http://localhost:9/v0.4/traces","agent_error":"","debug":false,"analytics_enabled":false,"sample_rate":"NaN","sampling_rules":null,"sampling_rules_error":"","service_mappings":null,"tags":{"runtime-id":"[^"]*"},"runtime_metrics_enabled":false,"health_metrics_enabled":false,"profiler_code_hotspots_enabled":((false)|(true)),"profiler_endpoints_enabled":((false)|(true)),"dd_version":"","architecture":"[^"]*","global_service":"","lambda_mode":"true","appsec":((true)|(false)),"agent_features":{"DropP0s":false,"Stats":false,"StatsdPort":0,"Info":false},"long_running_enabled":((true)|(false))}`, tp.Lines()[0])
	})
}

func TestLogSamplingRules(t *testing.T) {
	assert := assert.New(t)
	tp := new(testLogger)
	os.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service", "sample_rate": 0.234}, {"service": "other.service"}, {"service": "last.service", "sample_rate": 0.56}, {"odd": "pairs"}, {"sample_rate": 9.10}]`)
	defer os.Unsetenv("DD_TRACE_SAMPLING_RULES")
	_, _, _, stop := startTestTracer(t, WithLogger(tp))
	defer stop()

	lines := removeAppSec(tp.Lines())
	assert.Len(lines, 2)
	assert.Contains(lines[0], "WARN: at index 4: ignoring rule {Service: Name: Rate:9.10}: rate is out of [0.0, 1.0] range")
	assert.Regexp(`Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+ WARN: DIAGNOSTICS Error\(s\) parsing DD_TRACE_SAMPLING_RULES: found errors:\n\tat index 1: rate not provided\n\tat index 3: rate not provided$`, lines[1])
}

func TestLogAgentReachable(t *testing.T) {
	assert := assert.New(t)
	tp := new(testLogger)
	tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
	defer stop()
	tp.Reset()
	logStartup(tracer)
	assert.Len(tp.Lines(), 2)
	assert.Regexp(`Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+ WARN: DIAGNOSTICS Unable to reach agent intake: Post`, tp.Lines()[0])
}

func TestLogFormat(t *testing.T) {
	assert := assert.New(t)
	tp := new(testLogger)
	tracer := newTracer(WithLogger(tp), WithRuntimeMetrics(), WithDebugMode(true))
	defer tracer.Stop()
	tp.Reset()
	tracer.StartSpan("test", ServiceName("test-service"), ResourceName("/"), WithSpanID(12345))
	assert.Len(tp.Lines(), 1)
	assert.Regexp(`Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+ DEBUG: Started Span: dd.trace_id="12345" dd.span_id="12345", Operation: test, Resource: /, Tags: map.*, map.*`, tp.Lines()[0])
}
