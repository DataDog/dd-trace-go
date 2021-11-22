// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
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
		r, err := http.NewRequest("GET", "/path?token=value", nil)
		assert.NoError(err)
		handler := func(w http.ResponseWriter, r *http.Request) {
			_, ok := w.(http.Hijacker)
			assert.False(ok)
			http.Error(w, "some error", http.StatusServiceUnavailable)
			called = true
		}
		TraceAndServe(http.HandlerFunc(handler), &ServeConfig{
			ResponseWriter: w,
			Request:        r,
			Service:        "service",
			Resource:       "resource",
		})
		spans := mt.FinishedSpans()
		span := spans[0]

		assert.True(called)
		assert.Len(spans, 1)
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("service", span.Tag(ext.ServiceName))
		assert.Equal("resource", span.Tag(ext.ResourceName))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("/path", span.Tag(ext.HTTPURL))
		assert.Equal("503", span.Tag(ext.HTTPCode))
		assert.Equal("503: Service Unavailable", span.Tag(ext.Error).(error).Error())
	})

	t.Run("query-params", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		called := false
		w := httptest.NewRecorder()
		r, err := http.NewRequest("GET", "/path?token=value&id=1", nil)
		assert.NoError(err)
		handler := func(w http.ResponseWriter, r *http.Request) {
			called = true
		}
		TraceAndServe(http.HandlerFunc(handler), &ServeConfig{
			ResponseWriter: w,
			Request:        r,
			Service:        "service",
			Resource:       "resource",
			QueryParams:    true,
		})
		spans := mt.FinishedSpans()

		assert.True(called)
		assert.Len(spans, 1)
		assert.Equal("/path?token=value&id=1", spans[0].Tag(ext.HTTPURL))
	})

	t.Run("Hijacker,Flusher,CloseNotifier", func(t *testing.T) {
		assert := assert.New(t)
		called := false
		handler := func(w http.ResponseWriter, r *http.Request) {
			_, ok := w.(http.Hijacker)
			assert.True(ok, "ResponseWriter should implement http.Hijacker")
			_, ok = w.(http.Flusher)
			assert.True(ok, "ResponseWriter should implement http.Flusher")
			_, ok = w.(http.CloseNotifier)
			assert.True(ok, "ResponseWriter should implement http.CloseNotifier")
			fmt.Fprintln(w, "Hello, world!")
			called = true
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			TraceAndServe(http.HandlerFunc(handler), &ServeConfig{
				ResponseWriter: w,
				Request:        r,
				Service:        "service",
				Resource:       "resource",
			})
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

	// there doesn't appear to be an easy way to test http.Pusher support via an http request
	// so we'll just confirm wrapResponseWriter preserves it
	t.Run("Pusher", func(t *testing.T) {
		var i struct {
			http.ResponseWriter
			http.Pusher
		}
		var w http.ResponseWriter = i
		_, ok := w.(http.ResponseWriter)
		assert.True(t, ok)
		_, ok = w.(http.Pusher)
		assert.True(t, ok)

		w = wrapResponseWriter(w, nil)
		_, ok = w.(http.ResponseWriter)
		assert.True(t, ok)
		_, ok = w.(http.Pusher)
		assert.True(t, ok)
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

		TraceAndServe(http.HandlerFunc(handler), &ServeConfig{
			ResponseWriter: w,
			Request:        r,
			Service:        "service",
			Resource:       "resource",
		})

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

		TraceAndServe(http.HandlerFunc(handler), &ServeConfig{
			ResponseWriter: w,
			Request:        r,
			Service:        "service",
			Resource:       "resource",
		})

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

	t.Run("doubleStatus", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		handler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.WriteHeader(http.StatusInternalServerError)
		}
		r, err := http.NewRequest("GET", "/", nil)
		assert.NoError(err)
		w := httptest.NewRecorder()
		TraceAndServe(http.HandlerFunc(handler), &ServeConfig{
			ResponseWriter: w,
			Request:        r,
			Service:        "service",
			Resource:       "resource",
		})

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		assert.Equal("200", spans[0].Tag(ext.HTTPCode))
	})
}

func TestTraceAndServeHost(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	t.Run("on", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		r, err := http.NewRequest("GET", "http://localhost/", nil)
		assert.NoError(err)

		TraceAndServe(handler, &ServeConfig{
			ResponseWriter: httptest.NewRecorder(),
			Request:        r,
			Service:        "service",
			Resource:       "resource",
		})
		span := mt.FinishedSpans()[0]

		assert.EqualValues("localhost", span.Tag("http.host"))
	})

	t.Run("off", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		r, err := http.NewRequest("GET", "/", nil)
		assert.NoError(err)
		TraceAndServe(handler, &ServeConfig{
			ResponseWriter: httptest.NewRecorder(),
			Request:        r,
			Service:        "service",
			Resource:       "resource",
		})
		span := mt.FinishedSpans()[0]

		assert.EqualValues(nil, span.Tag("http.host"))
	})
}

type noopHandler struct{}

func (noopHandler) ServeHTTP(_ http.ResponseWriter, _ *http.Request) {}

type noopWriter struct{}

func (w noopWriter) Header() http.Header         { return nil }
func (w noopWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w noopWriter) WriteHeader(_ int)           {}

func BenchmarkTraceAndServe(b *testing.B) {
	handler := new(noopHandler)
	req, err := http.NewRequest("POST", "http://localhost:8181/widgets", nil)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		cfg := ServeConfig{
			ResponseWriter: noopWriter{},
			Request:        req,
			Service:        "service-name",
			Resource:       "resource-name",
			FinishOpts:     []ddtrace.FinishOption{},
			SpanOpts:       []ddtrace.StartSpanOption{},
			QueryParams:    false,
		}
		TraceAndServe(handler, &cfg)
	}
}
