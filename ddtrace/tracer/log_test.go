// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package tracer

import (
	"math"
	"net/http"
	"os"
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
		assert.Len(tp.Lines(), 2)
		assert.Regexp(`Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+ INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":false,"analytics_enabled":false,"sample_rate":"NaN","sampling_rules":\[\],"sampling_rules_error":"","tags":{},"runtime_metrics_enabled":false,"health_metrics_enabled":false,"dd_version":"","architecture":"[^"]*","global_service":""}`, tp.Lines()[1])
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
		logStartup(tracer)
		assert.Len(tp.Lines(), 2)
		assert.Regexp(`Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+ INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"configuredEnv","service":"configured.service","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":true,"analytics_enabled":true,"sample_rate":"0\.123000","sampling_rules":\[{"service":"mysql","name":"","sample_rate":0\.75}\],"sampling_rules_error":"","tags":{"tag":"value","tag2":"NaN"},"runtime_metrics_enabled":true,"health_metrics_enabled":true,"dd_version":"2.3.4","architecture":"[^"]*","global_service":"configured.service"}`, tp.Lines()[1])
	})

	t.Run("errors", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(testLogger)
		os.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service", "sample_rate": 0.234}, {"service": "other.service"}]`)
		defer os.Unsetenv("DD_TRACE_SAMPLING_RULES")
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
		defer stop()
		tracer.transport = newHTTPTransport("localhost:9", http.DefaultClient)

		tp.Reset()
		logStartup(tracer)
		assert.Len(tp.Lines(), 3)
		assert.Regexp(`Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+ INFO: DATADOG TRACER CONFIGURATION {"date":"[^"]*","os_name":"[^"]*","os_version":"[^"]*","version":"[^"]*","lang":"Go","lang_version":"[^"]*","env":"","service":"tracer\.test","agent_url":"http://localhost:9/v0.4/traces","agent_error":"Post .*","debug":false,"analytics_enabled":false,"sample_rate":"NaN","sampling_rules":\[{"service":"some.service","name":"","sample_rate":0\.234}\],"sampling_rules_error":"at index 1: rate not provided","tags":{},"runtime_metrics_enabled":false,"health_metrics_enabled":false,"dd_version":"","architecture":"[^"]*","global_service":""}`, tp.Lines()[2])
	})
}

func TestLogSamplingRules(t *testing.T) {
	t.Run("partial", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(testLogger)
		os.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service", "sample_rate": 0.234}, {"service": "other.service"}, {"service": "last.service", "sample_rate": 0.56}, {"odd": "pairs"}, {"sample_rate": 9.10}]`)
		defer os.Unsetenv("DD_TRACE_SAMPLING_RULES")
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
		defer stop()

		tp.Reset()
		logStartup(tracer)
		assert.Regexp(`Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+ WARN: DATADOG TRACER DIAGNOSTICS Error\(s\) parsing DD_TRACE_SAMPLING_RULES: at index 1: rate not provided, at index 3: rate not provided$`, tp.Lines()[0])
	})

	t.Run("fail", func(t *testing.T) {
		assert := assert.New(t)
		tp := new(testLogger)
		os.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service", "sample_rate": "invalid"}, {"service": "other.service"}, {"service": "last.service", "sample_rate": 0.56}]`)
		defer os.Unsetenv("DD_TRACE_SAMPLING_RULES")
		tracer, _, _, stop := startTestTracer(t, WithLogger(tp))
		defer stop()

		tp.Reset()
		logStartup(tracer)
		assert.Regexp(`Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+ WARN: DATADOG TRACER DIAGNOSTICS Error\(s\) parsing DD_TRACE_SAMPLING_RULES: error unmarshalling JSON: json: invalid number literal, trying to unmarshal "\\"invalid\\"" into Number$`, tp.Lines()[0])
	})
}
