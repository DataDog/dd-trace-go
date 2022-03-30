package tracer

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"testing"
)

func TestQueryTextCarrier(t *testing.T) {
	testCases := []struct {
		name      string
		query     string
		tags      map[string]string
		commented string
	}{
		{
			name:      "query with tag list",
			query:     "SELECT * from FOO",
			tags:      map[string]string{"service": "mine", "operation": "checkout"},
			commented: "/* bg-operation='checkout',bg-service='mine',span-id='1',trace-id='1',x-datadog-sampling-priority='1' */ SELECT * from FOO",
		},
		{
			name:      "empty query",
			query:     "",
			tags:      map[string]string{"service": "mine", "operation": "elmer's glue"},
			commented: "",
		},
		{
			name:      "query with existing comment",
			query:     "SELECT * from FOO -- test query",
			tags:      map[string]string{"service": "mine", "operation": "elmer's glue"},
			commented: "/* bg-operation='elmer%27s%20glue',bg-service='mine',span-id='1',trace-id='1',x-datadog-sampling-priority='1' */ SELECT * from FOO -- test query",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			propagator := NewPropagator(&PropagatorConfig{
				BaggagePrefix: "bg-",
				TraceHeader:   "trace-id",
				ParentHeader:  "span-id",
			})
			tracer := newTracer(WithPropagator(propagator))

			root := tracer.StartSpan("web.request", WithSpanID(1)).(*span)
			for k, v := range tc.tags {
				root.SetBaggageItem(k, v)
			}

			root.SetTag(ext.SamplingPriority, 1)
			ctx := root.Context()

			carrier := SQLCommentCarrier{}
			err := tracer.Inject(ctx, &carrier)
			require.NoError(t, err)

			assert.Equal(t, tc.commented, carrier.CommentedQuery(tc.query))
		})
	}
}
