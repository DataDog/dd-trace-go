package tracer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
)

func TestTag(t *testing.T) {
	for name, tc := range map[string]struct {
		key  string
		val  interface{}
		want interface{}
	}{
		"name":     {key: ext.SpanName, val: "http.request", want: "http.request"},
		"service":  {key: ext.ServiceName, val: "test_svc", want: "test_svc"},
		"resource": {key: ext.ResourceName, val: "GET /example", want: "GET /example"},
		"type":     {key: ext.SpanType, val: "web", want: "web"},

		"manual-keep":                   {key: ext.ManualKeep, val: true, want: true},
		"manual-keep-v1":                {key: keySamplingPriority, val: 2, want: float64(2)},
		"manual-keep-sampling-priority": {key: ext.SamplingPriority, val: 2, want: float64(2)},
		"manual-drop":                   {key: ext.ManualDrop, val: true, want: true},
		"manual-drop-v1":                {key: keySamplingPriority, val: -1, want: float64(-1)},
		"manual-drop-sampling-priority": {key: ext.SamplingPriority, val: -1, want: float64(-1)},

		"error-msg":     {key: ext.ErrorMsg, val: "abc", want: "abc"},
		"error-type":    {key: ext.ErrorType, val: "*errors.errorString", want: "*errors.errorString"},
		"error-stack":   {key: ext.ErrorStack, val: "stack", want: "stack"},
		"error-details": {key: ext.ErrorDetails, val: "error-details", want: "error-details"},

		"meta":              {key: "custom", val: "value", want: "value"},
		"float64":           {key: "user.id", val: 1234, want: float64(1234)},
		"bool-true":         {key: "test_true", val: true, want: "true"},
		"bool-false":        {key: "test_false", val: false, want: "false"},
		"bool-true-string":  {key: "test_true_string", val: "true", want: "true"},
		"bool-false-string": {key: "test_false_string", val: "false", want: "false"},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			span := newBasicSpan("")
			s := readWriteSpan{span}
			span.SetTag(tc.key, tc.val)
			assert.Equal(tc.want, s.Tag(tc.key))
		})
	}

	t.Run("no-tag", func(t *testing.T) {
		assert := assert.New(t)
		span := newBasicSpan("")
		s := readWriteSpan{span}
		assert.Equal(nil, s.Tag("no-tag"))
	})
}

func TestErrorGetter(t *testing.T) {
	t.Run("no-error", func(t *testing.T) {
		assert := assert.New(t)
		span := newBasicSpan("http.request")
		s := readWriteSpan{span}
		assert.Equal(false, s.IsError())
	})

	t.Run("no-error", func(t *testing.T) {
		assert := assert.New(t)
		span := newBasicSpan("http.request")
		s := readWriteSpan{span}
		span.SetTag(ext.Error, errors.New("abc"))
		assert.Equal(true, s.IsError())
	})
}

func TestSetTag(t *testing.T) {
	for name, tc := range map[string]struct {
		key      string
		val      interface{}
		rWSetVal interface{}
		want     interface{}
	}{
		"name":              {key: ext.SpanName, val: "http.request", rWSetVal: "changed.name"},
		"type":              {key: ext.SpanType, val: "web", rWSetVal: "db"},
		"resource":          {key: ext.ResourceName, val: "GET /example", rWSetVal: "POST /datadog"},
		"service":           {key: ext.ServiceName, val: "test_svc", rWSetVal: "new_svc"},
		"status_code":       {key: ext.HTTPCode, val: "200", rWSetVal: "404"},
		"env":               {key: ext.Environment, val: "prod", rWSetVal: "breaking"},
		"measured":          {key: keyMeasured, val: 0, rWSetVal: 1, want: float64(0)},
		"keyTopLevel":       {key: keyTopLevel, val: 0, rWSetVal: 1, want: float64(0)},
		"ManualKeep":        {key: ext.ManualKeep, val: true, rWSetVal: false},
		"ManualDrop":        {key: ext.ManualDrop, val: true, rWSetVal: false},
		"analytics":         {key: ext.AnalyticsEvent, val: true, rWSetVal: true, want: true},
		"analytics-rate":    {key: ext.EventSampleRate, val: 0, rWSetVal: 1, want: float64(0)},
		"sampling-priority": {key: ext.SamplingPriority, val: 1, rWSetVal: 4, want: float64(1)},
		"sampling-v1":       {key: keySamplingPriority, val: 1, rWSetVal: 4, want: float64(1)},
	} {
		t.Run(name, func(t *testing.T) {
			assert := assert.New(t)
			span := newBasicSpan("")
			s := readWriteSpan{span}
			span.SetTag(tc.key, tc.val)
			s.SetTag(tc.key, tc.rWSetVal)
			if tc.want == nil {
				assert.Equal(tc.val, s.Tag(tc.key))
			} else {
				assert.Equal(tc.want, s.Tag(tc.key))
			}
		})
	}

	t.Run("allowed-tags", func(t *testing.T) {
		assert := assert.New(t)
		span := newBasicSpan("")
		s := readWriteSpan{span}
		s.SetTag("custom_string", "value")
		assert.Equal("value", s.Tag("custom_string"))
		s.SetTag("custom_int", 1234)
		assert.Equal(float64(1234), s.Tag("custom_int"))
		s.SetTag("custom_bool", true)
		assert.Equal("true", s.Tag("custom_bool"))
	})
}

func TestNewReadWriteSpanSlice(t *testing.T) {
	assert := assert.New(t)
	spans := []*span{newBasicSpan("http.request"), newBasicSpan("db.request")}
	rWSpans := newReadWriteSpanSlice(spans)
	for i, s := range rWSpans {
		assert.Equal(s.(readWriteSpan).span, spans[i])
		assert.Same(s.(readWriteSpan).span, spans[i])
	}
}

func TestRunProcessor(t *testing.T) {
	t.Run("accept", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithPostProcessor(func([]ddtrace.ReadWriteSpan) bool { return true }))
		defer stop()
		spans := []*span{newBasicSpan("http.request"), newBasicSpan("db.request")}
		assert.Equal(true, tracer.runProcessor(spans))
	})

	t.Run("accept-condition", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithPostProcessor(func(spans []ddtrace.ReadWriteSpan) bool {
			for _, span := range spans {
				if span.Tag(ext.SpanName) == "accept.request" {
					return true
				}
			}
			return false
		}))
		defer stop()
		spans := []*span{newBasicSpan("http.request"), newBasicSpan("accept.request")}
		assert.Equal(true, tracer.runProcessor(spans))
	})

	t.Run("reject", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithPostProcessor(func([]ddtrace.ReadWriteSpan) bool { return false }))
		defer stop()
		spans := []*span{newBasicSpan("http.request"), newBasicSpan("db.request")}
		assert.Equal(false, tracer.runProcessor(spans))
	})

	t.Run("reject-condition", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithPostProcessor(func(spans []ddtrace.ReadWriteSpan) bool {
			for _, span := range spans {
				if span.Tag(ext.SpanName) == "reject.request" {
					return false
				}
			}
			return true
		}))
		defer stop()
		spans := []*span{newBasicSpan("http.request"), newBasicSpan("reject.request")}
		assert.Equal(false, tracer.runProcessor(spans))
	})

	t.Run("empty-spans", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithPostProcessor(func(spans []ddtrace.ReadWriteSpan) bool { return true }))
		defer stop()
		spans := []*span{}
		assert.Equal(true, tracer.runProcessor(spans))
		assert.Equal(0, len(spans))
	})

	t.Run("tag", func(t *testing.T) {
		assert := assert.New(t)
		tracer, _, _, stop := startTestTracer(t, WithPostProcessor(func(spans []ddtrace.ReadWriteSpan) bool {
			for _, span := range spans {
				if span.Tag(ext.SpanName) == "http.request" {
					span.SetTag("custom", "val")
				}
				if span.Tag(ext.SpanName) == "db.request" {
					span.SetTag("metric", float64(1))
				}
			}
			return true
		}))
		defer stop()
		spans := []*span{newBasicSpan("http.request"), newBasicSpan("db.request")}
		assert.Equal(true, tracer.runProcessor(spans))
		for _, span := range spans {
			if span.Name == "http.request" {
				assert.Equal("val", span.Meta["custom"])
				assert.Equal(float64(0), span.Metrics["metric"])
			}
			if span.Name == "db.request" {
				assert.Equal("", span.Meta["custom"])
				assert.Equal(float64(1), span.Metrics["metric"])
			}
		}
	})
}

func runProcessorTestEndToEnd(t *testing.T, testFunc func(sls spanLists), processor func([]ddtrace.ReadWriteSpan) bool, startSpans func()) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var sls spanLists
		if err := msgp.Decode(r.Body, &sls); err != nil {
			t.Fatal(err)
		}
		testFunc(sls)
	}))
	defer srv.Close()

	// Disables call to checkEndpoint which sends an empty payload.
	os.Setenv("DD_TRACE_STARTUP_LOGS", "false")
	defer os.Unsetenv("DD_TRACE_STARTUP_LOGS")
	transport := newDefaultTransport()
	httpTransport, ok := transport.(*httpTransport)
	if !ok {
		t.Fatal("transport type assertion failed")
	}
	// Setting the traceURL manually instead of WithAgentAddr because
	// the latter would force us to have an endpoint "/v0.4/traces",
	// which is subject to change.
	httpTransport.traceURL = srv.URL
	Start(
		WithPostProcessor(processor),
		withTransport(transport),
	)
	defer Stop()
	startSpans()
}

func TestProcessorEndToEnd(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		runProcessorTestEndToEnd(t,
			func(sls spanLists) {
				assert.Equal(t, 3, len(sls))
				for _, spanList := range sls {
					assert.Equal(t, 1, len(spanList))
					for _, span := range spanList {
						assert.Equal(t, "accepted.req", span.Name)
					}
				}
			},
			func(spans []ddtrace.ReadWriteSpan) bool { return true },
			func() {
				span1 := StartSpan("accepted.req")
				span1.Finish()
				span2 := StartSpan("accepted.req")
				span2.Finish()
				span3 := StartSpan("accepted.req")
				span3.Finish()
			},
		)
	})

	t.Run("rejected", func(t *testing.T) {
		runProcessorTestEndToEnd(t,
			func(sls spanLists) {
				assert.Equal(t, 1, len(sls))
				for _, spanList := range sls {
					assert.Equal(t, 1, len(spanList))
					for _, span := range spanList {
						assert.Equal(t, "accepted.req", span.Name)
					}
				}
			},
			func(spans []ddtrace.ReadWriteSpan) bool {
				for _, span := range spans {
					if span.Tag(ext.SpanName) == "reject.req" {
						return false
					}
				}
				return true
			},
			func() {
				span1 := StartSpan("reject.req")
				span1.Finish()
				span2 := StartSpan("accepted.req")
				span2.Finish()
				span3 := StartSpan("reject.req")
				span3.Finish()
			})
	})

	t.Run("no-payload", func(t *testing.T) {
		runProcessorTestEndToEnd(t,
			func(sls spanLists) { t.Fatal("no payloads should be received") },
			func(spans []ddtrace.ReadWriteSpan) bool {
				for _, span := range spans {
					if span.Tag(ext.SpanName) == "reject.req" {
						return false
					}
				}
				return true
			},
			func() {
				span1, ctx := StartSpanFromContext(context.Background(), "http.req")
				span1Child, _ := StartSpanFromContext(ctx, "reject.req")
				span1Child.Finish()
				span1.Finish()
				span2 := StartSpan("reject.req")
				span2.Finish()
				span3 := StartSpan("reject.req")
				span3.Finish()
			})
	})

	t.Run("tagged", func(t *testing.T) {
		runProcessorTestEndToEnd(t,
			func(sls spanLists) {
				assert.Equal(t, 1, len(sls))
				for _, spanList := range sls {
					assert.Equal(t, 2, len(spanList))
					for _, span := range spanList {
						if span.Name == "tagged.req" {
							assert.Equal(t, "true", span.Meta["processor_tag"])
						} else {
							assert.Equal(t, "", span.Meta["processor_tag"])
						}
					}
				}
			},
			func(spans []ddtrace.ReadWriteSpan) bool {
				for _, span := range spans {
					if span.Tag(ext.SpanName) == "tagged.req" {
						span.SetTag("processor_tag", "true")
					}
				}
				return true
			},
			func() {
				parent, ctx := StartSpanFromContext(context.Background(), "accepted.req")
				child, _ := StartSpanFromContext(ctx, "tagged.req")
				child.Finish()
				parent.Finish()
			},
		)
	})

	t.Run("no-processor", func(t *testing.T) {
		runProcessorTestEndToEnd(t,
			func(sls spanLists) {
				assert.Equal(t, 2, len(sls))
				for _, spanList := range sls {
					assert.Equal(t, 2, len(spanList))
					for _, span := range spanList {
						assert.Equal(t, span.Name, "http.req")
					}
				}
			},
			nil,
			func() {
				parent, ctx := StartSpanFromContext(context.Background(), "http.req")
				child, _ := StartSpanFromContext(ctx, "http.req")
				child.Finish()
				parent.Finish()

				parent2, ctx := StartSpanFromContext(context.Background(), "http.req")
				child2, _ := StartSpanFromContext(ctx, "http.req")
				child2.Finish()
				parent2.Finish()
			},
		)
	})
}
