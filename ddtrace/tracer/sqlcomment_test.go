package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
)

func TestQueryTextCarrier(t *testing.T) {
	testCases := []struct {
		name      string
		query     string
		options   []InjectionOption
		commented string
	}{
		{
			name:      "all tags injected",
			query:     "SELECT * from FOO",
			options:   []InjectionOption{WithParentVersionKey("ddsv"), WithEnvironmentKey("dde"), WithServiceNameKey("ddsn"), WithSpanIDKey("ddsid"), WithTraceIDKey("ddtid"), WithSamplingPriorityKey("ddsp")},
			commented: "/*dde='test-env',ddsid='10',ddsn='whiskey-service',ddsp='2',ddsv='1.0.0',ddtid='10'*/ SELECT * from FOO",
		},
		{
			name:      "empty query, all tags injected",
			query:     "",
			options:   []InjectionOption{WithParentVersionKey("ddsv"), WithEnvironmentKey("dde"), WithServiceNameKey("ddsn"), WithSpanIDKey("ddsid"), WithTraceIDKey("ddtis"), WithSamplingPriorityKey("ddsp")},
			commented: "/*dde='test-env',ddsid='10',ddsn='whiskey-service',ddsp='2',ddsv='1.0.0',ddtis='10'*/",
		},
		{
			name:      "query with existing comment",
			query:     "SELECT * from FOO -- test query",
			options:   []InjectionOption{WithParentVersionKey("ddsv"), WithEnvironmentKey("dde"), WithServiceNameKey("ddsn"), WithSpanIDKey("ddsid"), WithTraceIDKey("ddtis"), WithSamplingPriorityKey("ddsp")},
			commented: "/*dde='test-env',ddsid='10',ddsn='whiskey-service',ddsp='2',ddsv='1.0.0',ddtis='10'*/ SELECT * from FOO -- test query",
		},
		{
			name:      "only parent version tag",
			query:     "SELECT * from FOO",
			options:   []InjectionOption{WithParentVersionKey("ddsv")},
			commented: "/*ddsv='1.0.0'*/ SELECT * from FOO",
		},
		{
			name:      "only env tag",
			query:     "SELECT * from FOO",
			options:   []InjectionOption{WithEnvironmentKey("dde")},
			commented: "/*dde='test-env'*/ SELECT * from FOO",
		},
		{
			name:      "only service name tag",
			query:     "SELECT * from FOO",
			options:   []InjectionOption{WithServiceNameKey("ddsn")},
			commented: "/*ddsn='whiskey-service'*/ SELECT * from FOO",
		},
		{
			name:      "only trace id tag",
			query:     "SELECT * from FOO",
			options:   []InjectionOption{WithTraceIDKey("ddtid")},
			commented: "/*ddtid='10'*/ SELECT * from FOO",
		},
		{
			name:      "only span id tag",
			query:     "SELECT * from FOO",
			options:   []InjectionOption{WithSpanIDKey("ddsid")},
			commented: "/*ddsid='10'*/ SELECT * from FOO",
		},
		{
			name:      "only sampling priority tag",
			query:     "SELECT * from FOO",
			options:   []InjectionOption{WithSamplingPriorityKey("ddsp")},
			commented: "/*ddsp='2'*/ SELECT * from FOO",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tracer := newTracer(WithService("whiskey-service"), WithEnv("test-env"), WithServiceVersion("1.0.0"))

			root := tracer.StartSpan("db.call", WithSpanID(10), ServiceName("whiskey-db")).(*span)
			root.SetTag(ext.SamplingPriority, 2)
			ctx := root.Context()

			carrier := SQLCommentCarrier{}
			err := tracer.InjectWithOptions(ctx, &carrier, tc.options...)
			require.NoError(t, err)

			assert.Equal(t, tc.commented, carrier.CommentedQuery(tc.query))
		})
	}
}
