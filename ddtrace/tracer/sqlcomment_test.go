// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"strconv"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLCommentPropagator(t *testing.T) {
	prepareSpanContextWithSpanID := func(tracer *tracer) ddtrace.SpanContext {
		root := tracer.StartSpan("service.calling.db", WithSpanID(10)).(*span)
		root.SetTag(ext.SamplingPriority, 2)
		return root.Context()
	}

	testCases := []struct {
		name               string
		query              string
		mode               SQLCommentInjectionMode
		prepareSpanContext func(*tracer) ddtrace.SpanContext
		expectedQuery      string
		expectedSpanIDGen  bool
	}{
		{
			name:               "all tags injected",
			query:              "SELECT * from FOO",
			mode:               SQLInjectionModeFull,
			prepareSpanContext: prepareSpanContextWithSpanID,
			expectedQuery:      "/*dde='test-env',ddsid='<span_id>',ddsn='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddsp='2',ddsv='1.0.0',ddtid='10'*/ SELECT * from FOO",
			expectedSpanIDGen:  true,
		},
		{
			name:  "no existing trace",
			query: "SELECT * from FOO",
			mode:  SQLInjectionModeFull,
			prepareSpanContext: func(tracer *tracer) ddtrace.SpanContext {
				return nil
			},
			expectedQuery:     "/*ddsid='<span_id>',ddsn='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddsp='0',ddtid='<span_id>'*/ SELECT * from FOO",
			expectedSpanIDGen: true,
		},
		{
			name:               "empty query, all tags injected",
			query:              "",
			mode:               SQLInjectionModeFull,
			prepareSpanContext: prepareSpanContextWithSpanID,
			expectedQuery:      "/*dde='test-env',ddsid='<span_id>',ddsn='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddsp='2',ddsv='1.0.0',ddtid='10'*/",
			expectedSpanIDGen:  true,
		},
		{
			name:               "query with existing comment",
			query:              "SELECT * from FOO -- test query",
			mode:               SQLInjectionModeFull,
			prepareSpanContext: prepareSpanContextWithSpanID,
			expectedQuery:      "/*dde='test-env',ddsid='<span_id>',ddsn='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddsp='2',ddsv='1.0.0',ddtid='10'*/ SELECT * from FOO -- test query",
			expectedSpanIDGen:  true,
		},
		{
			name:               "static tags only mode",
			query:              "SELECT * from FOO",
			mode:               SQLInjectionModeService,
			prepareSpanContext: prepareSpanContextWithSpanID,
			expectedQuery:      "/*dde='test-env',ddsn='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddsv='1.0.0'*/ SELECT * from FOO",
			expectedSpanIDGen:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// the test service name includes all RFC3986 reserved characters to make sure all of them are url encoded
			// as per the sqlcommenter spec
			tracer := newTracer(WithService("whiskey-service !#$%&'()*+,/:;=?@[]"), WithEnv("test-env"), WithServiceVersion("1.0.0"))

			ctx := tc.prepareSpanContext(tracer)
			carrier := SQLCommentCarrier{Query: tc.query, Mode: tc.mode}
			err := carrier.Inject(ctx)
			require.NoError(t, err)

			expected := strings.ReplaceAll(tc.expectedQuery, "<span_id>", strconv.FormatUint(carrier.SpanID, 10))
			assert.Equal(t, expected, carrier.Query)
		})
	}
}
