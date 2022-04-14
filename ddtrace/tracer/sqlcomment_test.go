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
			tags:      map[string]string{"operation": "checkout"},
			commented: "/*ddsid='10',ddsp='2',ddtid='10',ot-baggage-operation='checkout'*/ SELECT * from FOO",
		},
		{
			name:      "empty query",
			query:     "",
			tags:      map[string]string{"operation": "elmer's glue"},
			commented: "",
		},
		{
			name:      "query with existing comment",
			query:     "SELECT * from FOO -- test query",
			tags:      map[string]string{"operation": "elmer's glue"},
			commented: "/*ddsid='10',ddsp='2',ddtid='10',ot-baggage-operation='elmer%27s%20glue'*/ SELECT * from FOO -- test query",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			propagator := NewPropagator(&PropagatorConfig{})
			tracer := newTracer(WithPropagator(propagator))

			root := tracer.StartSpan("web.request", WithSpanID(10)).(*span)
			for k, v := range tc.tags {
				root.SetBaggageItem(k, v)
			}

			root.SetTag(ext.SamplingPriority, 2)
			ctx := root.Context()

			carrier := SQLCommentCarrier{}
			err := tracer.Inject(ctx, &carrier)
			require.NoError(t, err)

			assert.Equal(t, tc.commented, carrier.CommentedQuery(tc.query))
		})
	}
}
