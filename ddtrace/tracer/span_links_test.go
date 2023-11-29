package tracer

import (
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"os"
	"strconv"
	"testing"
)

func TestWithSpanLinks(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	defer tracer.Stop()

	parent := tracer.StartSpan("parent")
	pTraceID := parent.(*span).context.traceID
	pSpanID := parent.Context().SpanID()
	defer parent.Finish()
	tts := []struct {
		name string
		in   ddtrace.SpanLink
		out  ddtrace.SpanLink
	}{
		{
			name: "",
			in: ddtrace.SpanLink{
				// if traceId & spanId specified explicitly,
				// they won't be overriden during validation
				TraceID: pTraceID.Lower(),
				SpanID:  pSpanID,
				Attributes: map[string]interface{}{
					"attributes": []string{"first", "second"},
					"discard":    nil,
				},
			},
			out: ddtrace.SpanLink{
				TraceID:     pTraceID.Lower(),
				TraceIDHigh: 0,
				SpanID:      pSpanID,
				Attributes: map[string]interface{}{
					"attributes.0": "first",
					"attributes.1": "second",
				},
			},
		},
		{
			name: "",
			in: ddtrace.SpanLink{
				Attributes: map[string]interface{}{
					"attributes": []string{"first", "second"},
					"discard":    nil,
				},
			},
			out: ddtrace.SpanLink{
				TraceID:     pTraceID.Lower(),
				TraceIDHigh: pTraceID.Upper(),
				SpanID:      pSpanID,
				Attributes: map[string]interface{}{
					"attributes.0": "first",
					"attributes.1": "second",
				},
			},
		},
	}

	for _, tt := range tts {
		t.Run(tt.name, func(t *testing.T) {
			child := tracer.StartSpan("db.query",
				ChildOf(parent.Context()),
				WithSpanLinks([]ddtrace.SpanLink{tt.in}))
			child.Finish()

			assert.Len(child.(*span).Links, 1)
			link := child.(*span).Links[0]
			assert.EqualValues(link.SpanID, tt.out.SpanID)
			assert.EqualValues(link.TraceID, tt.out.TraceID)
			assert.EqualValues(link.TraceIDHigh, tt.out.TraceIDHigh)
			assert.Equal(len(tt.out.Attributes), len(link.Attributes))
			for k, v := range tt.out.Attributes {
				assert.EqualValues(v, link.Attributes[k])
			}
		})
	}

}

func TestSpanLinkFromContextPropagation(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	defer tracer.Stop()

	tts := []struct {
		propagationStyle string
		name             string
		in               TextMapCarrier
		out              ddtrace.SpanLink
	}{
		{
			name:             "datadog",
			propagationStyle: "datadog",
			in: TextMapCarrier{
				DefaultTraceIDHeader:  "123456789",
				DefaultParentIDHeader: "987654321",
				DefaultPriorityHeader: "-2",
				originHeader:          "test.origin",
			},
			out: ddtrace.SpanLink{
				TraceID: 123456789,
				SpanID:  987654321,
				Attributes: map[string]interface{}{
					"attributes.0": "first",
					"attributes.1": "second",
				},
			},
		},
		{
			name:             "tracecontext",
			propagationStyle: "tracecontext",
			in: TextMapCarrier{
				traceparentHeader: "00-12345678901234567890123456789012-1234567890123456-01",
				tracestateHeader:  "dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
			},
			out: ddtrace.SpanLink{
				TraceID:     8687463697196027922,
				TraceIDHigh: 1311768467284833366,
				SpanID:      1311768467284833366,
				Attributes: map[string]interface{}{
					"attributes.0": "first",
					"attributes.1": "second",
				},
				Tracestate:        "dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
				Flags:             1,
				DroppedAttributes: 0,
			},
		},
	}

	for _, tt := range tts {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("DD_TRACE_PROPAGATION_STYLE", tt.propagationStyle)
			ctx, err := tracer.Extract(tt.in)
			if err != nil {
				t.FailNow()
			}
			child := tracer.StartSpan("db.query",
				ChildOf(ctx),
				WithSpanLinks([]ddtrace.SpanLink{{
					Attributes: map[string]interface{}{
						"attributes": []string{"first", "second"},
						"discard":    nil,
					}}}))
			child.Finish()

			assert.Len(child.(*span).Links, 1)
			link := child.(*span).Links[0]
			assert.EqualValues(link.SpanID, tt.out.SpanID)
			assert.EqualValues(link.TraceID, tt.out.TraceID)
			assert.EqualValues(link.TraceIDHigh, tt.out.TraceIDHigh)
			assert.EqualValues(link.Tracestate, tt.out.Tracestate)
			assert.EqualValues(link.Flags, tt.out.Flags)
			assert.Equal(len(tt.out.Attributes), len(link.Attributes))
			for k, v := range tt.out.Attributes {
				assert.EqualValues(v, link.Attributes[k])
			}
		})
	}

}

func TestSpanLinkTraceIDWith128Bits(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	defer tracer.Stop()

	tts := []struct {
		traceID128enabled int
		name              string
		out               ddtrace.SpanLink
	}{
		{
			name: "default",
			out: ddtrace.SpanLink{
				// 128-bits is on by default, TraceIDHigh must be generated
				Attributes: map[string]interface{}{
					"attributes.0": "first",
					"attributes.1": "second",
				},
			},
		},
		{
			name:              "enabled",
			traceID128enabled: 1,
			out: ddtrace.SpanLink{
				Attributes: map[string]interface{}{
					"attributes.0": "first",
					"attributes.1": "second",
				},
			},
		},
		{
			name:              "disabled",
			traceID128enabled: 1,
			out: ddtrace.SpanLink{
				Attributes: map[string]interface{}{
					"attributes.0": "first",
					"attributes.1": "second",
				},
			},
		},
	}

	for _, tt := range tts {
		t.Run(tt.name, func(t *testing.T) {
			enabled := tt.traceID128enabled == 1
			if tt.traceID128enabled != 0 {
				os.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", strconv.FormatBool(enabled))
			}
			parent := tracer.StartSpan("parent")
			pTraceID := parent.(*span).context.traceID
			pSpanID := parent.Context().SpanID()
			defer parent.Finish()

			child := tracer.StartSpan("db.query",
				ChildOf(parent.Context()),
				WithSpanLinks([]ddtrace.SpanLink{{
					Attributes: map[string]interface{}{
						"attributes": []string{"first", "second"},
						"discard":    nil,
					}}}))
			child.Finish()

			assert.Len(child.(*span).Links, 1)
			link := child.(*span).Links[0]
			assert.EqualValues(link.SpanID, pSpanID)
			assert.EqualValues(link.TraceID, pTraceID.Lower())
			if enabled {
				assert.NotEqual(link.TraceIDHigh, 0)
			}
			assert.EqualValues(link.TraceIDHigh, pTraceID.Upper())
		})
	}

}

func TestSpanLinkAttributesToArray(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	defer tracer.Stop()

	child := tracer.StartSpan("db.query",
		WithSpanLinks([]ddtrace.SpanLink{{
			TraceID:     1,
			TraceIDHigh: 0,
			SpanID:      1,
			Attributes: map[string]interface{}{
				"strings": []string{"1", "2"},
				"ints":    []int{1, 2},
				"misc":    []interface{}{0, "0", nil},
				"arr":     [][]int{{1, 2}},
			},
			Tracestate:        "",
			Flags:             0,
			DroppedAttributes: 0,
		}}))

	child.Finish()

	link1 := child.(*span).Links[0]
	assert.EqualValues(8, len(link1.Attributes))
	assert.EqualValues("1", link1.Attributes["strings.0"])
	assert.EqualValues("2", link1.Attributes["strings.1"])
	assert.EqualValues(1, link1.Attributes["ints.0"])
	assert.EqualValues(2, link1.Attributes["ints.1"])
	assert.EqualValues(0, link1.Attributes["misc.0"])
	assert.EqualValues("0", link1.Attributes["misc.1"])
	assert.EqualValues("<nil>", link1.Attributes["misc.2"])
	assert.EqualValues("[1 2]", link1.Attributes["arr.0"])
}

func TestSpanLinkPriority(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	defer tracer.Stop()

	parentOne := tracer.StartSpan("parent").(*span)
	defer parentOne.Finish()

	parentTwo := tracer.StartSpan("parent").(*span)
	defer parentOne.Finish()

	child := tracer.StartSpan("db.query",
		ChildOf(parentOne.Context()),
		WithSpanLinks([]ddtrace.SpanLink{{
			Attributes: map[string]interface{}{
				"first_link": []string{"1", "2", "3"},
				"discard":    nil,
			}}}))

	child.AddLinks(NewSpanLink(parentTwo.Context(), map[string]interface{}{
		"second_link": []int{2, 1},
	}))
	child.Finish()

	links := child.(*span).Links
	assert.Len(links, 2)

	link1 := links[0]
	assert.EqualValues(link1.SpanID, parentOne.Context().SpanID())
	assert.EqualValues(link1.TraceID, parentOne.context.traceID.Lower())
	assert.EqualValues(link1.TraceIDHigh, parentOne.context.traceID.Upper())
	assert.Equal(3, len(link1.Attributes))
	assert.Equal(link1.Attributes["first_link.0"], "1")
	assert.Equal(link1.Attributes["first_link.1"], "2")
	assert.Equal(link1.Attributes["first_link.2"], "3")

	link2 := links[1]
	assert.EqualValues(link2.SpanID, parentTwo.Context().SpanID())
	assert.EqualValues(link2.TraceID, parentTwo.context.traceID.Lower())
	assert.EqualValues(link2.TraceIDHigh, parentTwo.context.traceID.Upper())
	assert.EqualValues(link2.Attributes["second_link.0"], 2)
	assert.EqualValues(link2.Attributes["second_link.1"], 1)
	assert.Equal(2, len(link2.Attributes))
}
