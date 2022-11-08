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
		name              string
		query             string
		mode              DBMPropagationMode
		injectSpan        bool
		samplingPriority  int
		expectedQuery     string
		expectedSpanIDGen bool
	}{
		{
			name:              "default",
			query:             "SELECT * from FOO",
			mode:              DBMPropagationModeFull,
			injectSpan:        true,
			expectedQuery:     "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-00'*/ SELECT * from FOO",
			expectedSpanIDGen: true,
		},
		{
			name:              "service",
			query:             "SELECT * from FOO",
			mode:              DBMPropagationModeService,
			injectSpan:        true,
			expectedQuery:     "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0'*/ SELECT * from FOO",
			expectedSpanIDGen: false,
		},
		{
			name:              "no-trace",
			query:             "SELECT * from FOO",
			mode:              DBMPropagationModeFull,
			expectedQuery:     "/*dddbs='whiskey-db',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',traceparent='00-0000000000000000<span_id>-<span_id>-00'*/ SELECT * from FOO",
			expectedSpanIDGen: true,
		},
		{
			name:              "no-query",
			query:             "",
			mode:              DBMPropagationModeFull,
			injectSpan:        true,
			expectedQuery:     "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-00'*/",
			expectedSpanIDGen: true,
		},
		{
			name:              "commented",
			query:             "SELECT * from FOO -- test query",
			mode:              DBMPropagationModeFull,
			injectSpan:        true,
			samplingPriority:  1,
			expectedQuery:     "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-01'*/ SELECT * from FOO -- test query",
			expectedSpanIDGen: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// the test service name includes all RFC3986 reserved characters to make sure all of them are url encoded
			// as per the sqlcommenter spec
			tracer := newTracer(WithService("whiskey-service !#$%&'()*+,/:;=?@[]"), WithEnv("test-env"), WithServiceVersion("1.0.0"))
			defer tracer.Stop()

			var spanCtx ddtrace.SpanContext
			if tc.injectSpan {
				root := tracer.StartSpan("service.calling.db", WithSpanID(10)).(*span)
				root.SetTag(ext.SamplingPriority, tc.samplingPriority)
				spanCtx = root.Context()
			}

			carrier := SQLCommentCarrier{Query: tc.query, Mode: tc.mode, DBServiceName: "whiskey-db"}
			err := carrier.Inject(spanCtx)
			require.NoError(t, err)
			expected := strings.ReplaceAll(tc.expectedQuery, "<span_id>", fmt.Sprintf("%016s", strconv.FormatUint(carrier.SpanID, 16)))
			assert.Equal(t, expected, carrier.Query)
		})
	}
}

func BenchmarkSQLCommentInjection(b *testing.B) {
	tracer := newTracer(WithService("whiskey-service !#$%&'()*+,/:;=?@[]"), WithEnv("test-env"), WithServiceVersion("1.0.0"))
	defer tracer.Stop()
	root := tracer.StartSpan("service.calling.db", WithSpanID(10)).(*span)
	root.SetTag(ext.SamplingPriority, 2)
	spanCtx := root.Context()
	carrier := SQLCommentCarrier{Query: "SELECT 1 FROM dual", Mode: DBMPropagationModeFull, DBServiceName: "whiskey-db"}

	b.ReportAllocs()
	for n := 0; n < b.N; n++ {
		carrier.Inject(spanCtx)
	}
}
