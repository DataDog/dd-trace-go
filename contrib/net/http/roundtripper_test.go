// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

func TestWrapRoundTripperAllowNilTransport(t *testing.T) {
	assert := assert.New(t)

	httpClient := &http.Client{}
	httpClient.Transport = WrapRoundTripper(httpClient.Transport)

	wrapped, ok := httpClient.Transport.(*roundTripper)
	assert.True(ok)

	assert.Equal(http.DefaultTransport, wrapped.base)
}

func TestRoundTripper(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header))
		assert.NoError(t, err)

		span := tracer.StartSpan("test",
			tracer.ChildOf(spanctx))
		defer span.Finish()

		w.Write([]byte("Hello World"))
	}))
	defer s.Close()

	rt := WrapRoundTripper(http.DefaultTransport,
		WithBefore(func(req *http.Request, span ddtrace.SpanW3C) {
			span.SetTag("CalledBefore", true)
		}),
		WithAfter(func(res *http.Response, span ddtrace.SpanW3C) {
			span.SetTag("CalledAfter", true)
		}))

	client := &http.Client{
		Transport: rt,
	}

	client.Get(s.URL + "/hello/world")

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID())

	s0 := spans[0]
	assert.Equal(t, "test", s0.OperationName())
	assert.Equal(t, "test", s0.Tag(ext.ResourceName))

	s1 := spans[1]
	assert.Equal(t, "http.request", s1.OperationName())
	assert.Equal(t, "http.request", s1.Tag(ext.ResourceName))
	assert.Equal(t, "200", s1.Tag(ext.HTTPCode))
	assert.Equal(t, "GET", s1.Tag(ext.HTTPMethod))
	assert.Equal(t, s.URL+"/hello/world", s1.Tag(ext.HTTPURL))
	assert.Equal(t, true, s1.Tag("CalledBefore"))
	assert.Equal(t, true, s1.Tag("CalledAfter"))
	assert.Equal(t, ext.SpanKindClient, s1.Tag(ext.SpanKind))
	assert.Equal(t, "net/http", s1.Tag(ext.Component))
}

func TestRoundTripperServerError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header))
		assert.NoError(t, err)

		span := tracer.StartSpan("test",
			tracer.ChildOf(spanctx))
		defer span.Finish()

		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Error"))
	}))
	defer s.Close()

	rt := WrapRoundTripper(http.DefaultTransport,
		WithBefore(func(req *http.Request, span ddtrace.SpanW3C) {
			span.SetTag("CalledBefore", true)
		}),
		WithAfter(func(res *http.Response, span ddtrace.SpanW3C) {
			span.SetTag("CalledAfter", true)
		}))

	client := &http.Client{
		Transport: rt,
	}

	client.Get(s.URL + "/hello/world")

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 2)
	assert.Equal(t, spans[0].TraceID(), spans[1].TraceID())

	s0 := spans[0]
	assert.Equal(t, "test", s0.OperationName())
	assert.Equal(t, "test", s0.Tag(ext.ResourceName))

	s1 := spans[1]
	assert.Equal(t, "http.request", s1.OperationName())
	assert.Equal(t, "http.request", s1.Tag(ext.ResourceName))
	assert.Equal(t, "500", s1.Tag(ext.HTTPCode))
	assert.Equal(t, "GET", s1.Tag(ext.HTTPMethod))
	assert.Equal(t, s.URL+"/hello/world", s1.Tag(ext.HTTPURL))
	assert.Equal(t, fmt.Errorf("500: Internal Server Error"), s1.Tag(ext.Error))
	assert.Equal(t, true, s1.Tag("CalledBefore"))
	assert.Equal(t, true, s1.Tag("CalledAfter"))
	assert.Equal(t, ext.SpanKindClient, s1.Tag(ext.SpanKind))
	assert.Equal(t, "net/http", s1.Tag(ext.Component))
}

func TestRoundTripperNetworkError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	done := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header))
		assert.NoError(t, err)
		<-done
	}))
	defer s.Close()
	defer close(done)

	rt := WrapRoundTripper(http.DefaultTransport,
		WithBefore(func(req *http.Request, span ddtrace.SpanW3C) {
			span.SetTag("CalledBefore", true)
		}),
		WithAfter(func(res *http.Response, span ddtrace.SpanW3C) {
			span.SetTag("CalledAfter", true)
		}))

	client := &http.Client{
		Transport: rt,
		Timeout:   1 * time.Millisecond,
	}

	client.Get(s.URL + "/hello/world")

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)

	s0 := spans[0]
	assert.Equal(t, "http.request", s0.OperationName())
	assert.Equal(t, "http.request", s0.Tag(ext.ResourceName))
	assert.Equal(t, nil, s0.Tag(ext.HTTPCode))
	assert.Equal(t, "GET", s0.Tag(ext.HTTPMethod))
	assert.Equal(t, s.URL+"/hello/world", s0.Tag(ext.HTTPURL))
	assert.NotNil(t, s0.Tag(ext.Error))
	assert.Equal(t, true, s0.Tag("CalledBefore"))
	assert.Equal(t, true, s0.Tag("CalledAfter"))
	assert.Equal(t, ext.SpanKindClient, s0.Tag(ext.SpanKind))
	assert.Equal(t, "net/http", s0.Tag(ext.Component))
}

func TestWrapClient(t *testing.T) {
	c := WrapClient(http.DefaultClient)
	assert.Equal(t, c, http.DefaultClient)
	_, ok := c.Transport.(*roundTripper)
	assert.True(t, ok)
}

func TestRoundTripperAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...RoundTripperOption) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		rt := WrapRoundTripper(http.DefaultTransport, opts...)

		client := &http.Client{Transport: rt}
		client.Get(srv.URL + "/hello/world")
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		s := spans[0]
		assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, RTWithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, RTWithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, RTWithAnalyticsRate(0.23))
	})
}

// TestRoundTripperCopy is a regression test ensuring that RoundTrip
// does not modify the request per the RoundTripper contract. See:
// https://cs.opensource.google/go/go/+/refs/tags/go1.18.1:src/net/http/client.go;l=129-133
func TestRoundTripperCopy(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header))
		assert.NoError(t, err)
		w.Write([]byte("Hello World"))
	}))
	defer s.Close()

	initialReq, err := http.NewRequest("GET", s.URL+"/hello/world", nil)
	assert.NoError(t, err)
	req, err := http.NewRequest("GET", s.URL+"/hello/world", nil)
	assert.NoError(t, err)
	rt := WrapRoundTripper(http.DefaultTransport).(*roundTripper)
	_, err = rt.RoundTrip(req)
	assert.NoError(t, err)
	assert.Len(t, req.Header, 0)
	assert.Equal(t, initialReq, req)
}

func TestRoundTripperIgnoreRequest(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World"))
	}))
	defer s.Close()

	rt := WrapRoundTripper(http.DefaultTransport, RTWithIgnoreRequest(
		func(req *http.Request) bool {
			return req.URL.Path == "/ignore"
		},
	)).(*roundTripper)

	ignoreReq, err := http.NewRequest("GET", s.URL+"/ignore", nil)
	assert.NoError(t, err)
	_, err = rt.RoundTrip(ignoreReq)
	assert.NoError(t, err)

	req, err := http.NewRequest("GET", s.URL+"/hello", nil)
	assert.NoError(t, err)
	_, err = rt.RoundTrip(req)
	assert.NoError(t, err)

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
}

func TestServiceName(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World"))
	}))
	defer s.Close()

	t.Run("option", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		serviceName := "testServer"
		rt := WrapRoundTripper(http.DefaultTransport, RTWithServiceName(serviceName))
		client := &http.Client{
			Transport: rt,
		}
		client.Get(s.URL + "/hello/world")
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		assert.Equal(t, serviceName, spans[0].Tag(ext.ServiceName))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		serviceName := "testServer"
		rt := WrapRoundTripper(http.DefaultTransport,
			RTWithServiceName("wrongServiceName"),
			WithBefore(func(_ *http.Request, span ddtrace.SpanW3C) {
				span.SetTag(ext.ServiceName, serviceName)
			}),
		)
		client := &http.Client{
			Transport: rt,
		}
		client.Get(s.URL + "/hello/world")
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		assert.Equal(t, serviceName, spans[0].Tag(ext.ServiceName))
	})
}

func TestResourceNamer(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World"))
	}))
	defer s.Close()

	t.Run("default", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		rt := WrapRoundTripper(http.DefaultTransport)
		client := &http.Client{
			Transport: rt,
		}
		client.Get(s.URL + "/hello/world")
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		assert.Equal(t, "http.request", spans[0].Tag(ext.ResourceName))
	})

	t.Run("custom", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		customNamer := func(req *http.Request) string {
			return fmt.Sprintf("%s %s", req.Method, req.URL.Path)
		}
		rt := WrapRoundTripper(http.DefaultTransport, RTWithResourceNamer(customNamer))
		client := &http.Client{
			Transport: rt,
		}
		client.Get(s.URL + "/hello/world")
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		assert.Equal(t, "GET /hello/world", spans[0].Tag(ext.ResourceName))
	})
}

func TestSpanOptions(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("")) }))
	defer s.Close()

	tagKey := "foo"
	tagValue := "bar"
	mt := mocktracer.Start()
	defer mt.Stop()
	rt := WrapRoundTripper(http.DefaultTransport, RTWithSpanOptions(tracer.Tag(tagKey, tagValue)))
	client := &http.Client{Transport: rt}

	client.Get(s.URL)

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
	assert.Equal(t, tagValue, spans[0].Tag(tagKey))
}
