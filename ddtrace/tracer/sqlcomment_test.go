package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

func TestQueryTextCarrier(t *testing.T) {
	testCases := []struct {
		name        string
		query       string
		mode        SQLCommentInjectionMode
		carrierOpts []SQLCommentCarrierOption
		commented   string
	}{
		{
			name:        "all tags injected",
			query:       "SELECT * from FOO",
			mode:        FullSQLCommentInjection,
			carrierOpts: nil,
			commented:   "/*dde='test-env',ddsid='10',ddsn='whiskey-service',ddsp='2',ddsv='1.0.0',ddtid='10'*/ SELECT * from FOO",
		},
		{
			name:        "empty query, all tags injected",
			query:       "",
			mode:        FullSQLCommentInjection,
			carrierOpts: nil,
			commented:   "/*dde='test-env',ddsid='10',ddsn='whiskey-service',ddsp='2',ddsv='1.0.0',ddtid='10'*/",
		},
		{
			name:        "query with existing comment",
			query:       "SELECT * from FOO -- test query",
			mode:        FullSQLCommentInjection,
			carrierOpts: nil,
			commented:   "/*dde='test-env',ddsid='10',ddsn='whiskey-service',ddsp='2',ddsv='1.0.0',ddtid='10'*/ SELECT * from FOO -- test query",
		},
		{
			name:        "discard dynamic tags",
			query:       "SELECT * from FOO",
			mode:        FullSQLCommentInjection,
			carrierOpts: []SQLCommentCarrierOption{CommentWithDynamicTagsDiscarded(true)},
			commented:   "/*dde='test-env',ddsn='whiskey-service',ddsv='1.0.0'*/ SELECT * from FOO",
		},
		{
			name:        "static tags only mode",
			query:       "SELECT * from FOO",
			mode:        StaticTagsSQLCommentInjection,
			carrierOpts: nil,
			commented:   "/*dde='test-env',ddsn='whiskey-service',ddsv='1.0.0'*/ SELECT * from FOO",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			propagator := NewCommentPropagator(tc.mode)
			tracer := newTracer(WithService("whiskey-service"), WithEnv("test-env"), WithServiceVersion("1.0.0"), WithPropagator(propagator))

			root := tracer.StartSpan("db.call", WithSpanID(10), ServiceName("whiskey-db")).(*span)
			root.SetTag(ext.SamplingPriority, 2)
			ctx := root.Context()

			carrier := NewSQLCommentCarrier(tc.carrierOpts...)
			err := tracer.Inject(ctx, carrier)
			require.NoError(t, err)

			commented, spanID := carrier.CommentQuery(tc.query)
			assert.Equal(t, tc.commented, commented)
			assert.Equal(t, uint64(0), spanID)
		})
	}
}
