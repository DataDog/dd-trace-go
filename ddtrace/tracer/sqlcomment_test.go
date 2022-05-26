package tracer

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

func TestSQLCommentPropagator(t *testing.T) {
	prepareSpanContextWithSpanID := func(tracer *tracer) ddtrace.SpanContext {
		root := tracer.StartSpan("db.call", WithSpanID(10), ServiceName("whiskey-db")).(*span)
		root.SetTag(ext.SamplingPriority, 2)
		return root.Context()
	}

	testCases := []struct {
		name               string
		query              string
		mode               SQLCommentInjectionMode
		carrierOpts        []SQLCommentCarrierOption
		prepareSpanContext func(*tracer) ddtrace.SpanContext
		expectedQuery      string
		expectedSpanIDGen  bool
	}{
		{
			name:               "all tags injected",
			query:              "SELECT * from FOO",
			mode:               FullSQLCommentInjection,
			carrierOpts:        nil,
			prepareSpanContext: prepareSpanContextWithSpanID,
			expectedQuery:      "/*dde='test-env',ddsid='10',ddsn='whiskey-service',ddsp='2',ddsv='1.0.0',ddtid='10'*/ SELECT * from FOO",
			expectedSpanIDGen:  false,
		},
		{
			name:        "no existing trace",
			query:       "SELECT * from FOO",
			mode:        FullSQLCommentInjection,
			carrierOpts: nil,
			prepareSpanContext: func(tracer *tracer) ddtrace.SpanContext {
				return nil
			},
			expectedQuery:     "/*ddsid='<span_id>',ddsn='whiskey-service',ddsp='0',ddtid='<span_id>'*/ SELECT * from FOO",
			expectedSpanIDGen: true,
		},
		{
			name:               "empty query, all tags injected",
			query:              "",
			mode:               FullSQLCommentInjection,
			carrierOpts:        nil,
			prepareSpanContext: prepareSpanContextWithSpanID,
			expectedQuery:      "/*dde='test-env',ddsid='10',ddsn='whiskey-service',ddsp='2',ddsv='1.0.0',ddtid='10'*/",
			expectedSpanIDGen:  false,
		},
		{
			name:               "query with existing comment",
			query:              "SELECT * from FOO -- test query",
			mode:               FullSQLCommentInjection,
			carrierOpts:        nil,
			prepareSpanContext: prepareSpanContextWithSpanID,
			expectedQuery:      "/*dde='test-env',ddsid='10',ddsn='whiskey-service',ddsp='2',ddsv='1.0.0',ddtid='10'*/ SELECT * from FOO -- test query",
			expectedSpanIDGen:  false,
		},
		{
			name:               "discard dynamic tags",
			query:              "SELECT * from FOO",
			mode:               FullSQLCommentInjection,
			carrierOpts:        []SQLCommentCarrierOption{SQLCommentWithDynamicTagsDiscarded(true)},
			prepareSpanContext: prepareSpanContextWithSpanID,
			expectedQuery:      "/*dde='test-env',ddsn='whiskey-service',ddsv='1.0.0'*/ SELECT * from FOO",
			expectedSpanIDGen:  false,
		},
		{
			name:               "static tags only mode",
			query:              "SELECT * from FOO",
			mode:               StaticTagsSQLCommentInjection,
			carrierOpts:        nil,
			prepareSpanContext: prepareSpanContextWithSpanID,
			expectedQuery:      "/*dde='test-env',ddsn='whiskey-service',ddsv='1.0.0'*/ SELECT * from FOO",
			expectedSpanIDGen:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			propagator := NewSQLCommentPropagator(tc.mode)
			tracer := newTracer(WithService("whiskey-service"), WithEnv("test-env"), WithServiceVersion("1.0.0"), WithPropagator(propagator))

			ctx := tc.prepareSpanContext(tracer)
			carrier := NewSQLCommentCarrier(tc.carrierOpts...)
			err := tracer.Inject(ctx, carrier)
			require.NoError(t, err)

			commented, spanID := carrier.CommentQuery(tc.query)
			if tc.expectedSpanIDGen {
				assert.Greater(t, spanID, uint64(0))
				expected := strings.ReplaceAll(tc.expectedQuery, "<span_id>", strconv.FormatUint(spanID, 10))
				assert.Equal(t, expected, commented)
			} else {
				assert.Equal(t, uint64(0), spanID)
				assert.Equal(t, tc.expectedQuery, commented)
			}
		})
	}
}
