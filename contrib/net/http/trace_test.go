// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
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
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, ok := w.(http.Hijacker)
			assert.False(ok)
			http.Error(w, "some error", http.StatusServiceUnavailable)
			called = true
		}
		TraceAndServe(http.HandlerFunc(handler), w, r, &ServeConfig{
			Service:  "service",
			Resource: "resource",
		})
		spans := mt.FinishedSpans()
		span := spans[0]

		assert.True(called)
		assert.Len(spans, 1)
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("service", span.Tag(ext.ServiceName))
		assert.Equal("resource", span.Tag(ext.ResourceName))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("/path?<redacted>", span.Tag(ext.HTTPURL))
		assert.Equal("503", span.Tag(ext.HTTPCode))
		assert.Equal("503: Service Unavailable", span.Tag(ext.ErrorMsg))
		assert.Equal("server", span.Tag(ext.SpanKind))
		assert.Equal("net/http", span.Tag(ext.Component))
	})

	t.Run("custom", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		called := false
		// w is a custom struct that *exclusively* implements ResponseWriter
		w := struct {
			http.ResponseWriter
		}{httptest.NewRecorder()}
		r, err := http.NewRequest("GET", "/path?token=value", nil)
		assert.NoError(err)
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, ok := w.(http.Hijacker)
			assert.False(ok)
			http.Error(w, "some error", http.StatusServiceUnavailable)
			called = true
		}
		TraceAndServe(http.HandlerFunc(handler), w, r, &ServeConfig{
			Service:  "service",
			Resource: "resource",
		})
		spans := mt.FinishedSpans()
		span := spans[0]

		assert.True(called)
		assert.Len(spans, 1)
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("service", span.Tag(ext.ServiceName))
		assert.Equal("resource", span.Tag(ext.ResourceName))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("/path?<redacted>", span.Tag(ext.HTTPURL))
		assert.Equal("503", span.Tag(ext.HTTPCode))
		assert.Equal("503: Service Unavailable", span.Tag(ext.ErrorMsg))
		assert.Equal("server", span.Tag(ext.SpanKind))
		assert.Equal("net/http", span.Tag(ext.Component))
	})

	t.Run("query-params", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		called := false
		w := httptest.NewRecorder()
		r, err := http.NewRequest("GET", "/path?token=value&id=1", nil)
		assert.NoError(err)
		handler := func(_ http.ResponseWriter, _ *http.Request) {
			called = true
		}
		TraceAndServe(http.HandlerFunc(handler), w, r, &ServeConfig{
			Service:     "service",
			Resource:    "resource",
			QueryParams: true,
		})
		spans := mt.FinishedSpans()

		assert.True(called)
		assert.Len(spans, 1)
		assert.Equal("/path?<redacted>&id=1", spans[0].Tag(ext.HTTPURL))
	})

	t.Run("Hijacker,Flusher,CloseNotifier", func(t *testing.T) {
		assert := assert.New(t)
		called := false
		handler := func(w http.ResponseWriter, _ *http.Request) {
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
			TraceAndServe(http.HandlerFunc(handler), w, r, &ServeConfig{
				Service:  "service",
				Resource: "resource",
			})
		}))
		defer srv.Close()

		res, err := http.Get(srv.URL)
		assert.NoError(err)
		slurp, err := io.ReadAll(res.Body)
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
		handler := func(_ http.ResponseWriter, _ *http.Request) {
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

		TraceAndServe(http.HandlerFunc(handler), w, r, &ServeConfig{
			Service:  "service",
			Resource: "resource",
		})

		var p, c *mocktracer.Span
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
		handler := func(_ http.ResponseWriter, _ *http.Request) {
			called = true
		}

		// create a request with a span in its context
		parent := tracer.StartSpan("parent")
		parent.Finish() // finish it so the mocktracer can catch it
		r, err := http.NewRequest("GET", "/", nil)
		assert.NoError(err)
		r = r.WithContext(tracer.ContextWithSpan(r.Context(), parent))
		w := httptest.NewRecorder()

		TraceAndServe(http.HandlerFunc(handler), w, r, &ServeConfig{
			Service:  "service",
			Resource: "resource",
		})

		var p, c *mocktracer.Span
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

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.WriteHeader(http.StatusInternalServerError)
		}
		r, err := http.NewRequest("GET", "/", nil)
		assert.NoError(err)
		w := httptest.NewRecorder()
		TraceAndServe(http.HandlerFunc(handler), w, r, &ServeConfig{
			Service:  "service",
			Resource: "resource",
		})

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		assert.Equal("200", spans[0].Tag(ext.HTTPCode))
	})

	t.Run("empty", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		called := false
		w := httptest.NewRecorder()
		r, err := http.NewRequest("GET", "/path?token=value", nil)
		assert.NoError(err)
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, ok := w.(http.Hijacker)
			assert.False(ok)
			called = true
		}
		TraceAndServe(http.HandlerFunc(handler), w, r, &ServeConfig{
			Service:  "service",
			Resource: "resource",
		})
		spans := mt.FinishedSpans()
		span := spans[0]

		assert.True(called)
		assert.Len(spans, 1)
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("service", span.Tag(ext.ServiceName))
		assert.Equal("resource", span.Tag(ext.ResourceName))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("/path?<redacted>", span.Tag(ext.HTTPURL))
		assert.Equal("200", span.Tag(ext.HTTPCode))
		assert.Equal("server", span.Tag(ext.SpanKind))
		assert.Equal("net/http", span.Tag(ext.Component))
	})

	t.Run("noconfig", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		called := false
		w := httptest.NewRecorder()
		r, err := http.NewRequest("GET", "/path?token=value", nil)
		assert.NoError(err)
		handler := func(w http.ResponseWriter, _ *http.Request) {
			_, ok := w.(http.Hijacker)
			assert.False(ok)
			called = true
		}
		TraceAndServe(http.HandlerFunc(handler), w, r, &ServeConfig{})
		spans := mt.FinishedSpans()
		span := spans[0]

		assert.True(called)
		assert.Len(spans, 1)
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("", span.Tag(ext.ServiceName)) // This is nil since mocktracer does not behave like the actual tracer, which will set a default.
		assert.Equal("http.request", span.Tag(ext.ResourceName))
		assert.Nil(span.Tag(ext.HTTPRoute))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("/path?<redacted>", span.Tag(ext.HTTPURL))
		assert.Equal("200", span.Tag(ext.HTTPCode))
		assert.Equal("server", span.Tag(ext.SpanKind))
		assert.Equal("net/http", span.Tag(ext.Component))
	})

	t.Run("override kind and component", func(t *testing.T) {
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
			called = true
		}
		customOpts := []tracer.StartSpanOption{tracer.Tag(ext.SpanKind, "custom.kind"), tracer.Tag(ext.Component, "custom.component")}
		TraceAndServe(http.HandlerFunc(handler), w, r, &ServeConfig{SpanOpts: customOpts})
		spans := mt.FinishedSpans()
		span := spans[0]

		assert.True(called)
		assert.Len(spans, 1)
		assert.Equal(ext.SpanTypeWeb, span.Tag(ext.SpanType))
		assert.Equal("", span.Tag(ext.ServiceName)) // This is nil since mocktracer does not behave like the actual tracer, which will set a default.
		assert.Equal("http.request", span.Tag(ext.ResourceName))
		assert.Nil(span.Tag(ext.HTTPRoute))
		assert.Equal("GET", span.Tag(ext.HTTPMethod))
		assert.Equal("/path?<redacted>", span.Tag(ext.HTTPURL))
		assert.Equal("200", span.Tag(ext.HTTPCode))
		assert.Equal("custom.kind", span.Tag(ext.SpanKind))
		assert.Equal("custom.component", span.Tag(ext.Component))
	})

	t.Run("integrationLevelErrorHandling", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
		}
		r, err := http.NewRequest("GET", "/", nil)
		assert.NoError(err)
		w := httptest.NewRecorder()
		TraceAndServe(http.HandlerFunc(handler), w, r, &ServeConfig{
			IsStatusError: func(i int) bool { return i >= 400 },
		})

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		assert.Equal("400", spans[0].Tag(ext.HTTPCode))
		assert.Equal("400: Bad Request", spans[0].Tag(ext.ErrorMsg))
	})

	t.Run("envLevelErrorHandling", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		t.Setenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES", "500")

		cfg := &ServeConfig{
			Service:  "service",
			Resource: "resource",
		}

		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError) // 500
		}

		r, err := http.NewRequest("GET", "/", nil)
		assert.NoError(err)
		w := httptest.NewRecorder()
		TraceAndServe(http.HandlerFunc(handler), w, r, cfg)

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		assert.Equal("500", spans[0].Tag(ext.HTTPCode))
		assert.Equal("500: Internal Server Error", spans[0].Tag(ext.ErrorMsg))
	})

	t.Run("integrationOverridesEnvConfig", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		// Set environment variable to treat 500 as an error
		t.Setenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES", "500")

		cfg := &ServeConfig{
			IsStatusError: func(i int) bool { return i == 400 },
		}

		// Test a 400 response, which should be reported as an error
		handler400 := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest) // 400
		}

		r400, err := http.NewRequest("GET", "/", nil)
		assert.NoError(err)
		w400 := httptest.NewRecorder()
		TraceAndServe(http.HandlerFunc(handler400), w400, r400, cfg)

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		assert.Equal("400", spans[0].Tag(ext.HTTPCode))
		assert.Equal("400: Bad Request", spans[0].Tag(ext.ErrorMsg))

		// Reset the tracer
		mt.Reset()

		// Test a 500 response, which should NOT be reported as an error,
		// even though the environment variable says 500 is an error.
		handler500 := func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError) // 500
		}

		r500, err := http.NewRequest("GET", "/", nil)
		assert.NoError(err)
		w500 := httptest.NewRecorder()
		TraceAndServe(http.HandlerFunc(handler500), w500, r500, cfg)

		spans = mt.FinishedSpans()
		assert.Len(spans, 1)
		assert.Equal("500", spans[0].Tag(ext.HTTPCode))
		// Confirm that the span is NOT marked as an error.
		assert.Nil(spans[0].Tag(ext.ErrorMsg))
	})
}

func TestTraceAndServeHost(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	t.Run("on", func(t *testing.T) {
		mt := mocktracer.Start()
		assert := assert.New(t)
		defer mt.Stop()

		r, err := http.NewRequest("GET", "http://localhost/", nil)
		assert.NoError(err)

		TraceAndServe(handler, httptest.NewRecorder(), r, &ServeConfig{
			Service:  "service",
			Resource: "resource",
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
		TraceAndServe(handler, httptest.NewRecorder(), r, &ServeConfig{
			Service:  "service",
			Resource: "resource",
		})
		span := mt.FinishedSpans()[0]

		assert.EqualValues(nil, span.Tag("http.host"))
	})
}

// TestUnwrap tests the implementation of the rwUnwrapper interface, which is used internally
// by the standard library: https://github.com/golang/go/blob/6d89b38ed86e0bfa0ddaba08dc4071e6bb300eea/src/net/http/responsecontroller.go#L42-L44
// See also: https://github.com/DataDog/dd-trace-go/issues/2674
func TestUnwrap(t *testing.T) {
	h := WrapHandler(deadlineHandler, "service-name", "resource-name")
	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	respText := string(b)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "OK", respText)
}

var deadlineHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	rc := http.NewResponseController(w)

	// Use the SetReadDeadline and SetWriteDeadline methods, which are not explicitly implemented in the trace_gen.go
	// generated file. Before the Unwrap change, these methods returned a "feature not supported" error.

	err := rc.SetReadDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}
	err = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
})

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
	cfg := ServeConfig{
		Service:     "service-name",
		Resource:    "resource-name",
		FinishOpts:  []tracer.FinishOption{},
		SpanOpts:    []tracer.StartSpanOption{},
		QueryParams: false,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		TraceAndServe(handler, noopWriter{}, req, &cfg)
	}
}
