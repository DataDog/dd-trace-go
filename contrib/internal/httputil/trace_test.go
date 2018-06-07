package httputil

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestTraceAndServe(t *testing.T) {
	t.Run("regular", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		called := false
		w := httptest.NewRecorder()
		r, err := http.NewRequest("GET", "http://localhost/", nil)
		assert.NoError(err)
		handler := func(w http.ResponseWriter, r *http.Request) {
			_, ok := w.(http.Hijacker)
			assert.False(ok)
			_, ok = w.(*responseWriter)
			assert.True(ok)
			http.Error(w, "some error", http.StatusServiceUnavailable)
			called = true
		}
		TraceAndServe(http.HandlerFunc(handler), w, r, "service", "resource")
		spans := mt.FinishedSpans()
		span := spans[0]

		assert.True(called)
		assert.Len(spans, 1)
		assert.Equal(ext.AppTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("service", span.Tag(ext.ServiceName))
		assert.Equal("resource", span.Tag(ext.ResourceName))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("/", span.Tag(ext.HTTPURL))
		assert.Equal("localhost", span.Tag(ext.TargetHost))
		assert.Nil(span.Tag(ext.TargetPort))
		assert.Equal("503", span.Tag(ext.HTTPCode))
		assert.Equal("503: Service Unavailable", span.Tag(ext.Error).(error).Error())
	})

	t.Run("hijackable", func(t *testing.T) {
		assert := assert.New(t)
		called := false
		handler := func(w http.ResponseWriter, r *http.Request) {
			_, ok := w.(http.Hijacker)
			assert.True(ok)
			_, ok = w.(*hijackableResponseWriter)
			assert.True(ok)
			fmt.Fprintln(w, "Hello, world!")
			called = true
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			TraceAndServe(http.HandlerFunc(handler), w, r, "service", "resource")
		}))
		defer srv.Close()

		res, err := http.Get(srv.URL)
		assert.NoError(err)
		slurp, err := ioutil.ReadAll(res.Body)
		res.Body.Close()
		assert.True(called)
		assert.NoError(err)
		assert.Equal("Hello, world!\n", string(slurp))
	})

	t.Run("distributed", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		called := false
		handler := func(w http.ResponseWriter, r *http.Request) {
			called = true
		}

		// create a request with a span injected into its headers
		parent := tracer.StartSpan("parent")
		parent.Finish() // finish it so the mocktracer can catch it
		r, err := http.NewRequest("GET", "/", nil)
		assert.NoError(err)
		carrier := tracer.HTTPHeadersCarrier(r.Header)
		err = tracer.Inject(parent.Context(), carrier)
		assert.NoError(err)
		w := httptest.NewRecorder()

		TraceAndServe(http.HandlerFunc(handler), w, r, "service", "resource")

		var p, c mocktracer.Span
		spans := mt.FinishedSpans()
		assert.Len(spans, 2)
		if spans[0].OperationName() == "parent" {
			p, c = spans[0], spans[1]
		} else {
			p, c = spans[1], spans[0]
		}
		assert.True(called)
		assert.Equal(c.ParentID(), p.SpanID())
	})

	t.Run("context", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		called := false
		handler := func(w http.ResponseWriter, r *http.Request) {
			called = true
		}

		// create a request with a span in its context
		parent := tracer.StartSpan("parent")
		parent.Finish() // finish it so the mocktracer can catch it
		r, err := http.NewRequest("GET", "/", nil)
		assert.NoError(err)
		r = r.WithContext(tracer.ContextWithSpan(r.Context(), parent))
		w := httptest.NewRecorder()

		TraceAndServe(http.HandlerFunc(handler), w, r, "service", "resource")

		var p, c mocktracer.Span
		spans := mt.FinishedSpans()
		assert.Len(spans, 2)
		if spans[0].OperationName() == "parent" {
			p, c = spans[0], spans[1]
		} else {
			p, c = spans[1], spans[0]
		}
		assert.True(called)
		assert.Equal(c.ParentID(), p.SpanID())
	})
}
