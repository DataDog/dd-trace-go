// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

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
		peerDBName         string
		peerDBHostname     string
		peerServiceName    string
		expectedQuery      string
		expectedSpanIDGen  bool
		expectedExtractErr error
	}{
		{
			name:               "default",
			query:              "SELECT * from FOO",
			mode:               DBMPropagationModeFull,
			injectSpan:         true,
			peerDBName:         "",
			peerDBHostname:     "",
			peerServiceName:    "",
			expectedQuery:      "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-00'*/ SELECT * from FOO",
			expectedSpanIDGen:  true,
			expectedExtractErr: nil,
		},
		{
			name:               "service",
			query:              "SELECT * from FOO",
			mode:               DBMPropagationModeService,
			injectSpan:         true,
			peerDBName:         "",
			peerDBHostname:     "",
			peerServiceName:    "",
			expectedQuery:      "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0'*/ SELECT * from FOO",
			expectedSpanIDGen:  false,
			expectedExtractErr: ErrSpanContextNotFound,
		},
		{
			name:               "no-trace",
			query:              "SELECT * from FOO",
			injectSpan:         false,
			mode:               DBMPropagationModeFull,
			peerDBName:         "",
			peerDBHostname:     "",
			peerServiceName:    "",
			expectedQuery:      "/*dddbs='whiskey-db',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',traceparent='00-0000000000000000<span_id>-<span_id>-00'*/ SELECT * from FOO",
			expectedSpanIDGen:  true,
			expectedExtractErr: nil,
		},
		{
			name:               "no-query",
			query:              "",
			mode:               DBMPropagationModeFull,
			injectSpan:         true,
			peerDBName:         "",
			peerDBHostname:     "",
			peerServiceName:    "",
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
			peerDBName:         "",
			peerDBHostname:     "",
			peerServiceName:    "",
			expectedQuery:      "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-01'*/ /* c */ SELECT * from FOO /**/",
			expectedSpanIDGen:  true,
			expectedExtractErr: nil,
		},
		{
			name:               "peer_entity_tags_dddb",
			query:              "/* c */ SELECT * from FOO /**/",
			mode:               DBMPropagationModeFull,
			injectSpan:         true,
			samplingPriority:   1,
			peerDBName:         "fake-database",
			peerDBHostname:     "",
			peerServiceName:    "",
			expectedQuery:      "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-01',dddb='fake-database'*/ /* c */ SELECT * from FOO /**/",
			expectedSpanIDGen:  true,
			expectedExtractErr: nil,
		},
		{
			name:               "peer_entity_tags_ddh",
			query:              "/* c */ SELECT * from FOO /**/",
			mode:               DBMPropagationModeFull,
			injectSpan:         true,
			samplingPriority:   1,
			peerDBName:         "",
			peerDBHostname:     "fake-hostname",
			peerServiceName:    "",
			expectedQuery:      "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-01',ddh='fake-hostname'*/ /* c */ SELECT * from FOO /**/",
			expectedSpanIDGen:  true,
			expectedExtractErr: nil,
		},
		{
			name:               "peer_entity_tags_dddb_and_ddh",
			query:              "/* c */ SELECT * from FOO /**/",
			mode:               DBMPropagationModeFull,
			injectSpan:         true,
			samplingPriority:   1,
			peerDBName:         "fake-database",
			peerDBHostname:     "fake-hostname",
			peerServiceName:    "",
			expectedQuery:      "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-01',ddh='fake-hostname',dddb='fake-database'*/ /* c */ SELECT * from FOO /**/",
			expectedSpanIDGen:  true,
			expectedExtractErr: nil,
		},
		{
			name:               "peer_entity_tags_peer_service",
			query:              "/* c */ SELECT * from FOO /**/",
			mode:               DBMPropagationModeFull,
			injectSpan:         true,
			samplingPriority:   1,
			peerDBName:         "",
			peerDBHostname:     "",
			peerServiceName:    "test-peer-service",
			expectedQuery:      "/*dddbs='whiskey-db',dde='test-env',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',ddpv='1.0.0',traceparent='00-0000000000000000000000000000000a-<span_id>-01',ddprs='test-peer-service'*/ /* c */ SELECT * from FOO /**/",
			expectedSpanIDGen:  true,
			expectedExtractErr: nil,
		},
	}

	// the test service name includes all RFC3986 reserved characters to make sure all of them are url encoded
	// as per the sqlcommenter spec
	tracer := newTracer(WithService("whiskey-service !#$%&'()*+,/:;=?@[]"), WithEnv("test-env"), WithServiceVersion("1.0.0"))
	defer tracer.Stop()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var spanCtx ddtrace.SpanContext
			var traceID uint64
			if tc.injectSpan {
				traceID = uint64(10)
				root := tracer.StartSpan("service.calling.db", WithSpanID(traceID))
				root.SetTag(ext.SamplingPriority, tc.samplingPriority)
				spanCtx = root.Context()
			}

			if tc.name == "no-trace" {
				t.Log()
			}
			carrier := SQLCommentCarrier{Query: tc.query, Mode: tc.mode, DBServiceName: "whiskey-db", PeerDBHostname: tc.peerDBHostname, PeerDBName: tc.peerDBName, PeerService: tc.peerServiceName}
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
				xctx, ok := sctx.(internal.SpanContextV2Adapter)
				require.True(t, ok)

				assert.Equal(t, carrier.SpanID, xctx.SpanID())
				assert.Equal(t, traceID, xctx.TraceID())

				p, ok := xctx.Ctx.SamplingPriority()
				assert.True(t, ok)
				assert.Equal(t, tc.samplingPriority, p)
			}
		})
	}
}

// https://github.com/DataDog/dd-trace-go/issues/2837
func TestSQLCommentCarrierInjectNilSpan(t *testing.T) {
	tracer := newTracer()
	defer tracer.Stop()

	headers := TextMapCarrier(map[string]string{
		DefaultTraceIDHeader:  "4",
		DefaultParentIDHeader: "1",
		originHeader:          "synthetics",
		b3TraceIDHeader:       "0021dc1807524785",
		traceparentHeader:     "00-00000000000000000000000000000004-2222222222222222-01",
		tracestateHeader:      "dd=s:2;o:rum;p:0000000000000001;t.tid:1230000000000000~~,othervendor=t61rcWkgMzE",
	})

	spanCtx, err := tracer.Extract(headers)
	require.NoError(t, err)

	carrier := SQLCommentCarrier{
		Query:          "SELECT * from FOO",
		Mode:           DBMPropagationModeFull,
		DBServiceName:  "whiskey-db",
		PeerDBHostname: "",
		PeerDBName:     "",
		PeerService:    "",
	}
	err = carrier.Inject(spanCtx)
	require.NoError(t, err)
}

func TestExtractOpenTelemetryTraceInformation(t *testing.T) {
	// open-telemetry supports 128 bit trace ids
	traceID := "5bd66ef5095369c7b0d1f8f4bd33716a"
	ss := "c532cb4098ac3dd2"
	upper, _ := strconv.ParseUint(traceID[:16], 16, 64)
	lower, _ := strconv.ParseUint(traceID[16:], 16, 64)
	spanID, _ := strconv.ParseUint(ss, 16, 64)
	ps := "1"
	priority, err := strconv.Atoi(ps)
	require.NoError(t, err)
	traceparent := fmt.Sprintf("00-%s-%s-0%s", traceID, ss, ps)
	// open-telemetry implementation appends comment to the end of the query
	q := "/*c*/ SELECT traceparent from FOO /**/ /*action='%2Fparam*d',controller='index,'framework='spring',traceparent='<trace-parent>',tracestate='congo%3Dt61rcWkgMzE%2Crojo%3D00f067aa0ba902b7'*/"
	q = strings.ReplaceAll(q, "<trace-parent>", traceparent)

	carrier := SQLCommentCarrier{Query: q}
	sctx, err := carrier.Extract()
	require.NoError(t, err)
	xctx, ok := sctx.(internal.SpanContextV2Adapter)
	assert.True(t, ok)

	assert.Equal(t, spanID, xctx.SpanID())
	assert.Equal(t, lower, xctx.TraceID())
	tID := xctx.TraceID128Bytes()
	assert.Equal(t, upper, binary.BigEndian.Uint64(tID[:8]))

	p, ok := xctx.Ctx.SamplingPriority()
	assert.True(t, ok)
	assert.Equal(t, priority, p)
}

func FuzzExtract(f *testing.F) {
	testCases := []struct {
		query string
	}{
		{"/*dddbs='whiskey-db',ddps='whiskey-service%20%21%23%24%25%26%27%28%29%2A%2B%2C%2F%3A%3B%3D%3F%40%5B%5D',traceparent='00-0000000000000000<span_id>-<span_id>-00'*/ SELECT * from FOO"},
		{"SELECT * from FOO -- test query"},
		{"/* c */ SELECT traceparent from FOO /**/"},
		{"/*c*/ SELECT traceparent from FOO /**/ /*action='%2Fparam*d',controller='index,'framework='spring',traceparent='<trace-parent>',tracestate='congo%3Dt61rcWkgMzE%2Crojo%3D00f067aa0ba902b7'*/"},
		{"*/ / * * *//*/**/"},
		{""},
	}
	for _, tc := range testCases {
		f.Add(tc.query)
	}
	f.Fuzz(func(t *testing.T, q string) {
		carrier := SQLCommentCarrier{Query: q}
		carrier.Extract() // make sure it doesn't panic
	})
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

func setupBenchmark() (ddtrace.Tracer, ddtrace.SpanContext, SQLCommentCarrier) {
	tracer := newTracer(WithService("whiskey-service !#$%&'()*+,/:;=?@[]"), WithEnv("test-env"), WithServiceVersion("1.0.0"))
	root := tracer.StartSpan("service.calling.db", WithSpanID(10))
	root.SetTag(ext.SamplingPriority, 2)
	spanCtx := root.Context()
	carrier := SQLCommentCarrier{Query: "SELECT 1 FROM dual", Mode: DBMPropagationModeFull, DBServiceName: "whiskey-db"}
	return tracer, spanCtx, carrier
}
