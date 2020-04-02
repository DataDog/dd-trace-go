// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package http

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

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

	listenIP, listenPort, _ := net.SplitHostPort(s.Listener.Addr().String())

	rt := WrapRoundTripper(http.DefaultTransport,
		WithBefore(func(req *http.Request, span ddtrace.Span) {
			span.SetTag("CalledBefore", true)
		}),
		WithAfter(func(res *http.Response, span ddtrace.Span) {
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
	assert.Equal(t, "/hello/world", s1.Tag("http.path"))
	assert.Contains(t, s1.Tag(ext.HTTPURL), "/hello/world")
	assert.Contains(t, s1.Tag(ext.HTTPURL), "http://")
	assert.Equal(t, listenIP, s1.Tag("network.destination.ip"))
	assert.Equal(t, listenPort, s1.Tag("network.destination.port"))
	assert.Equal(t, "text/plain; charset=utf-8", s1.Tag("http.content_type"))
	assert.Equal(t, int64(0), s1.Tag("http.dns_lookup_time"))
	assert.Less(t, s1.Tag("http.pretransfer_time"), int64(1000000))
	assert.Less(t, s1.Tag("http.starttransfer_time"), int64(1000000))
	assert.Equal(t, false, s1.Tag("http.is_tls"))
	assert.Equal(t, true, s1.Tag("CalledBefore"))
	assert.Equal(t, true, s1.Tag("CalledAfter"))
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
			WithBefore(func(_ *http.Request, span ddtrace.Span) {
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
