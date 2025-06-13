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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"

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

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// the test service name includes all RFC3986 reserved characters to make sure all of them are url encoded
			// as per the sqlcommenter spec
			tracer, err := newTracer(WithService("whiskey-service !#$%&'()*+,/:;=?@[]"), WithEnv("test-env"), WithServiceVersion("1.0.0"))
			defer globalconfig.SetServiceName("")
			defer tracer.Stop()
			assert.NoError(t, err)

			var spanCtx *SpanContext
			var traceID uint64
			if tc.injectSpan {
				traceID = uint64(10)
				root := tracer.StartSpan("service.calling.db", WithSpanID(traceID))
				root.setSamplingPriority(tc.samplingPriority, samplernames.Default)
				spanCtx = root.Context()
			}

			carrier := SQLCommentCarrier{Query: tc.query, Mode: tc.mode, DBServiceName: "whiskey-db", PeerDBHostname: tc.peerDBHostname, PeerDBName: tc.peerDBName, PeerService: tc.peerServiceName}
			err = carrier.Inject(spanCtx)
			require.NoError(t, err)
			expected := strings.ReplaceAll(tc.expectedQuery, "<span_id>", fmt.Sprintf("%016s", strconv.FormatUint(carrier.SpanID, 16)))
			assert.Equal(t, expected, carrier.Query)
			if !tc.injectSpan {
				traceID = carrier.SpanID
			}

			sctx, err := carrier.Extract()

			assert.Equal(t, tc.expectedExtractErr, err)

			if tc.expectedExtractErr == nil {
				assert.Equal(t, carrier.SpanID, sctx.spanID)
				assert.Equal(t, traceID, sctx.traceID.Lower())

				p, ok := sctx.SamplingPriority()
				assert.True(t, ok)
				assert.Equal(t, tc.samplingPriority, p)
			}
		})
	}
}

// https://github.com/DataDog/dd-trace-go/issues/2837
func TestSQLCommentCarrierInjectNilSpan(t *testing.T) {
	tracer, err := newTracer()
	require.NoError(t, err)
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

	assert.Equal(t, spanID, sctx.spanID)
	assert.Equal(t, lower, sctx.traceID.Lower())
	assert.Equal(t, upper, sctx.traceID.Upper())

	p, ok := sctx.SamplingPriority()
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
	f.Fuzz(func(_ *testing.T, q string) {
		carrier := SQLCommentCarrier{Query: q}
		carrier.Extract() // make sure it doesn't panic
	})
}

func FuzzSpanContextFromTraceComment(f *testing.F) {
	f.Fuzz(func(t *testing.T, query string, traceID uint64, spanID uint64, sampled int64) {
		expectedSampled := 0
		if sampled > 0 {
			expectedSampled = 1
		}

		ts := strconv.FormatUint(traceID, 16)
		var b strings.Builder
		b.Grow(32)
		for i := 0; i < 32-len(ts); i++ {
			b.WriteRune('0')
		}
		b.WriteString(ts)
		ts = b.String()

		traceIDUpper, _ := strconv.ParseUint(ts[:16], 16, 64)
		traceIDLower, err := strconv.ParseUint(ts[16:], 16, 64)
		if err != nil {
			t.Skip()
		}

		tags := make(map[string]string)
		comment := encodeTraceParent(traceID, spanID, int64(expectedSampled))
		tags[sqlCommentTraceParent] = comment
		q := commentQuery(query, tags)

		c, found := findTraceComment(q)
		if !found {
			t.Fatalf("Error parsing trace comment from query")
		}

		xctx, err := spanContextFromTraceComment(c)

		if err != nil {
			t.Fatalf("Error: %+v creating span context from trace comment: %s", err, c)
		}
		if xctx.spanID != spanID {
			t.Fatalf(`Inconsistent span id parsing:
				got: %d
				wanted: %d`, xctx.spanID, spanID)
		}
		if xctx.traceID.Lower() != traceIDLower {
			t.Fatalf(`Inconsistent lower trace id parsing:
				got: %d
				wanted: %d`, xctx.traceID.Lower(), traceIDLower)
		}
		if xctx.traceID.Upper() != traceIDUpper {
			t.Fatalf(`Inconsistent lower trace id parsing:
				got: %d
				wanted: %d`, xctx.traceID.Upper(), traceIDUpper)
		}

		p, ok := xctx.SamplingPriority()
		if !ok {
			t.Fatalf("Error retrieving sampling priority")
		}
		if p != expectedSampled {
			t.Fatalf(`Inconsistent trace id parsing:
				got: %d
				wanted: %d`, p, expectedSampled)
		}
	})
}

func TestCommentQueryNoTags(t *testing.T) {
	query := "SELECT * FROM table"
	result := commentQuery(query, map[string]string{})
	require.Equal(t, query, result)
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

func setupBenchmark() (*tracer, *SpanContext, SQLCommentCarrier) {
	tracer, _ := newTracer(WithService("whiskey-service !#$%&'()*+,/:;=?@[]"), WithEnv("test-env"), WithServiceVersion("1.0.0"))
	root := tracer.StartSpan("service.calling.db", WithSpanID(10))
	root.SetTag(ext.ManualKeep, true)
	spanCtx := root.Context()
	carrier := SQLCommentCarrier{Query: "SELECT 1 FROM dual", Mode: DBMPropagationModeFull, DBServiceName: "whiskey-db"}
	return tracer, spanCtx, carrier
}
