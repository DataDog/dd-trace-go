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

const logPrefixRegexp = `Datadog Tracer v[0-9]+\.[0-9]+\.[0-9]+(-(rc\.[0-9]+|dev))?`

func TestLogSamplingRules(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)
	tp.Ignore("appsec: ", "telemetry")
	t.Setenv("DD_TRACE_SAMPLING_RULES", `[{"service": "some.service", "sample_rate": 0.234}, {"service": "other.service"}, {"service": "last.service", "sample_rate": 0.56}, {"odd": "pairs"}, {"sample_rate": 9.10}]`)
	_, stop := startTestTracer(t, WithLogger(tp), WithEnv("test"))
	defer stop()

	assert.Len(tp.Logs(), 1)
	expected := logPrefixRegexp + ` WARN: DIAGNOSTICS Error\(s\) parsing sampling rules: found errors: \n\tat index 4: ignoring rule {Rate:9\.10}: rate is out of \[0\.0, 1\.0] range$`
	assert.Regexp(expected, tp.Logs()[0])
}

func TestLogFormat(t *testing.T) {
	assert := assert.New(t)
	tp := new(log.RecordLogger)

	tracer, stop := startTestTracer(t, WithLogger(tp), WithRuntimeMetrics(), WithDebugMode(true))
	defer stop()
	tp.Reset()
	tp.Ignore("appsec: ", "telemetry")
	tracer.StartSpan("test", ServiceName("test-service"), ResourceName("/"), WithSpanID(12345))
	assert.Len(tp.Logs(), 1)
	assert.Regexp(logPrefixRegexp+` DEBUG: Started Span: dd.trace_id="12345" dd.span_id="12345" dd.parent_id="0", Operation: test, Resource: /, Tags: map.*, map.*`, tp.Logs()[0])
}
