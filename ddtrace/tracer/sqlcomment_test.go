// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLCommentCarrier(t *testing.T) {
	testCases := []struct {
		name               string
		query              string
		mode               DBMPropagationMode
		injectSpan         bool
		samplingPriority   int
		expectedQuery      string
		expectedSpanIDGen  bool
		expectedExtractErr error
	}{
		{
			name:               "default",
			query:              "SELECT * from FOO",
			mode:               DBMPropagationModeFull,
			injectSpan:         true,
			expectedQuery:      "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-00'*/ SELECT * from FOO",
			expectedSpanIDGen:  true,
			expectedExtractErr: nil,
		},
		{
			name:               "service",
			query:              "SELECT * from FOO",
			mode:               DBMPropagationModeService,
			injectSpan:         true,
			expectedQuery:      "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0'*/ SELECT * from FOO",
			expectedSpanIDGen:  false,
			expectedExtractErr: ErrSpanContextNotFound,
		},
		{
			name:               "no-trace",
			query:              "SELECT * from FOO",
			mode:               DBMPropagationModeFull,
			expectedQuery:      "/*dddbs='whiskey-db',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',traceparent='00-0000000000000000<span_id>-<span_id>-00'*/ SELECT * from FOO",
			expectedSpanIDGen:  true,
			expectedExtractErr: nil,
		},
		{
			name:               "no-query",
			query:              "",
			mode:               DBMPropagationModeFull,
			injectSpan:         true,
			expectedQuery:      "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-00'*/",
			expectedSpanIDGen:  true,
			expectedExtractErr: nil,
		},
		{
			name:               "commented",
			query:              "SELECT * from FOO -- test query",
			mode:               DBMPropagationModeFull,
			injectSpan:         true,
			samplingPriority:   1,
			expectedQuery:      "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-01'*/ SELECT * from FOO -- test query",
			expectedSpanIDGen:  true,
			expectedExtractErr: nil,
		},
		{
			name:               "disabled",
			query:              "SELECT * from FOO",
			mode:               DBMPropagationModeDisabled,
			injectSpan:         true,
			samplingPriority:   1,
			expectedQuery:      "SELECT * from FOO",
			expectedSpanIDGen:  true,
			expectedExtractErr: ErrSpanContextNotFound,
		},
		{
			name:               "comment",
			query:              "/* c */ SELECT * from FOO /**/",
			mode:               DBMPropagationModeFull,
			injectSpan:         true,
			samplingPriority:   1,
			expectedQuery:      "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-01'*/ /* c */ SELECT * from FOO /**/",
			expectedSpanIDGen:  true,
			expectedExtractErr: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// the test service name includes all RFC3986 reserved characters to make sure all of them are url encoded
			// as per the sqlcommenter spec
			tracer := newTracer(WithService("whiskey-service !#$%&'()*+,/:;=?@[]"), WithEnv("test-env"), WithServiceVersion("1.0.0"))
			defer tracer.Stop()

			var spanCtx ddtrace.SpanContext
			var traceID uint64
			if tc.injectSpan {
				traceID = uint64(10)
				root := tracer.StartSpan("service.calling.db", WithSpanID(traceID)).(*span)
				root.SetTag(ext.SamplingPriority, tc.samplingPriority)
				spanCtx = root.Context()
			}

			carrier := SQLCommentCarrier{Query: tc.query, Mode: tc.mode, DBServiceName: "whiskey-db"}
			err := carrier.Inject(spanCtx)
			require.NoError(t, err)
			expected := strings.ReplaceAll(tc.expectedQuery, "<span_id>", fmt.Sprintf("%016s", strconv.FormatUint(carrier.SpanID, 16)))
			assert.Equal(t, expected, carrier.Query)

			if !tc.injectSpan {
				traceID = carrier.SpanID
			}

			sctx, err := carrier.Extract()

			assert.Equal(t, tc.expectedExtractErr, err)

			if tc.expectedExtractErr == nil {
				xctx, ok := sctx.(*spanContext)
				assert.True(t, ok)

				assert.Equal(t, carrier.SpanID, xctx.spanID)
				assert.Equal(t, traceID, xctx.traceID.Lower())

				p, ok := xctx.samplingPriority()
				assert.True(t, ok)
				assert.Equal(t, tc.samplingPriority, p)
			}
		})
	}
}

func TestExtractOpenTelemetryTraceInformation(t *testing.T) {
	spanID := generateSpanID(now())
	traceID := generateSpanID(now())
	priority := 1
	traceparent := encodeTraceParent(traceID, spanID, int64(priority))
	// open-telemetry implementation appends comment to the end of the query
	q := fmt.Sprintf("/*c*/ SELECT * from FOO /**/ /*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service',ddpv='1.0.0',traceparent='%s'*/", traceparent)
	carrier := SQLCommentCarrier{Query: q}

	sctx, err := carrier.Extract()
	require.NoError(t, err)
	xctx, ok := sctx.(*spanContext)
	assert.True(t, ok)

	assert.Equal(t, spanID, xctx.spanID)
	assert.Equal(t, traceID, xctx.traceID.Lower())

	p, ok := xctx.samplingPriority()
	assert.True(t, ok)
	assert.Equal(t, priority, p)
}

func BenchmarkSQLCommentInjection(b *testing.B) {
	tracer, spanCtx, carrier := setupBenchmark()
	defer tracer.Stop()

	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		carrier.Inject(spanCtx)
	}
}

func BenchmarkSQLCommentExtraction(b *testing.B) {
	tracer, spanCtx, carrier := setupBenchmark()
	defer tracer.Stop()
	carrier.Inject(spanCtx)

	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		carrier.Extract()
	}
}

func setupBenchmark() (*tracer, ddtrace.SpanContext, SQLCommentCarrier) {
	tracer := newTracer(WithService("whiskey-service !#$%&'()*+,/:;=?@[]"), WithEnv("test-env"), WithServiceVersion("1.0.0"))
	root := tracer.StartSpan("service.calling.db", WithSpanID(10)).(*span)
	root.SetTag(ext.SamplingPriority, 2)
	spanCtx := root.Context()
	carrier := SQLCommentCarrier{Query: "SELECT 1 FROM dual", Mode: DBMPropagationModeFull, DBServiceName: "whiskey-db"}
	return tracer, spanCtx, carrier
}
