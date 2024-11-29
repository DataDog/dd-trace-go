// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/stretchr/testify/assert"
)

const logPrefixRegexp = `Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+(-((rc|beta)\.[0-9]+|dev))?`

func TestLogSamplingRules(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tp.Ignore("appsec: ", "Instrumentation telemetry: ")
	t.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service", "sample_rate": 0.234}, {"service": "other.service"}, {"service": "last.service", "sample_rate": 0.56}, {"odd": "pairs"}, {"sample_rate": 9.10}]`)
	_, stop := startTestTracer(t, WithLogger(tp), WithEnv("test"))
	defer stop()

	assert.Len(tp.Logs(), 1)
	assert.Regexp(logPrefixRegexp+` WARN: DIAGNOSTICS Error\(s\) parsing sampling rules: found errors:\n\tat index 4: ignoring rule {Rate:9\.10}: rate is out of \[0\.0, 1\.0] range$`, tp.Logs()[0])
}

func TestLogFormat(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)

	tracer, stop := startTestTracer(t, WithLogger(tp), WithRuntimeMetrics(), WithDebugMode(true))
	defer stop()
	tp.Reset()
	tp.Ignore("appsec: ", "Instrumentation telemetry: ")
	tracer.StartSpan("test", ServiceName("test-service"), ResourceName("/"), WithSpanID(12345))
	assert.Len(tp.Logs(), 1)
	assert.Regexp(logPrefixRegexp+` DEBUG: Started Span: dd.trace_id="12345" dd.span_id="12345" dd.parent_id="0", Operation: test, Resource: /, Tags: map.*, map.*`, tp.Logs()[0])
}

func TestLogPropagators(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		substring := `"propagation_style_inject":"datadog,tracecontext","propagation_style_extract":"datadog,tracecontext"`
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

func (p *prop) Inject(context ddtrace.SpanContext, carrier interface{}) (e error) {
	return
}
func (p *prop) Extract(carrier interface{}) (sctx ddtrace.SpanContext, e error) {
	return
}

func setup(t *testing.T, customProp Propagator) string {
	tp := new(log.RecordLogger)
	var tracer *tracer
	var stop func()
	if customProp != nil {
		tracer, _, _, stop = startTestTracer(t, WithLogger(tp), WithPropagator(customProp))
	} else {
		tracer, _, _, stop = startTestTracer(t, WithLogger(tp))
	}
	defer stop()
	tp.Reset()
	tp.Ignore("appsec: ", telemetry.LogPrefix)
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
	tracer := newTracer(WithLogger(tp), WithUDS("var/run/datadog/apm.socket"))
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore("appsec: ", telemetry.LogPrefix)
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
	tracer := newTracer(WithLogger(tp))
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore("appsec: ", telemetry.LogPrefix)
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
	tracer := newTracer(WithLogger(tp))
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore("appsec: ", telemetry.LogPrefix)
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
	tracer := newTracer(WithLogger(tp), WithUDS("var/run/datadog/apm.socket"), WithAgentAddr("localhost:8126"))
	defer tracer.Stop()
	tp.Reset()
	tp.Ignore("appsec: ", telemetry.LogPrefix)
	logStartup(tracer)
	logEntry, found := findLogEntry(tp.Logs(), `"agent_url":"http://localhost:8126/v0.4/traces"`)
	if !found {
		t.Fatal("Expected to find log entry")
	}
	assert.Regexp(`"agent_url":"http://localhost:8126/v0.4/traces"`, logEntry)
}
