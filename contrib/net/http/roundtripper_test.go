// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internal "github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func setOTelSemantics(t *testing.T, value string) {
	t.Helper()
	oldValue, wasSet := os.LookupEnv("DD_TRACE_OTEL_SEMANTICS_ENABLED")
	if value == "" {
		require.NoError(t, os.Unsetenv("DD_TRACE_OTEL_SEMANTICS_ENABLED"))
	} else {
		require.NoError(t, os.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", value))
	}
	require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
	t.Cleanup(func() {
		if wasSet {
			require.NoError(t, os.Setenv("DD_TRACE_OTEL_SEMANTICS_ENABLED", oldValue))
		} else {
			require.NoError(t, os.Unsetenv("DD_TRACE_OTEL_SEMANTICS_ENABLED"))
		}
		require.NoError(t, tracer.Start(tracer.WithTraceEnabled(false)))
		tracer.Stop()
	})
}

func roundTripSpan(t *testing.T, base http.RoundTripper, req *http.Request, opts ...RoundTripperOption) (*mocktracer.Span, *http.Response) {
	t.Helper()
	mt := mocktracer.Start()
	defer mt.Stop()

	resp, err := WrapRoundTripper(base, opts...).RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0], resp
}

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
		WithBefore(func(_ *http.Request, span *tracer.Span) {
			span.SetTag("CalledBefore", true)
		}),
		WithAfter(func(_ *http.Response, span *tracer.Span) {
			span.SetTag("CalledAfter", true)
		}))

	client := &http.Client{
		Transport: rt,
	}

	resp, err := client.Get(s.URL + "/hello/world")
	assert.Nil(t, err)
	defer resp.Body.Close()

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
	assert.Equal(t, "true", s1.Tag("CalledBefore"))
	assert.Equal(t, "true", s1.Tag("CalledAfter"))
	assert.Equal(t, ext.SpanKindClient, s1.Tag(ext.SpanKind))
	assert.Equal(t, "net/http", s1.Tag(ext.Component))
	assert.Equal(t, "127.0.0.1", s1.Tag(ext.NetworkDestinationName))

	wantPort, err := strconv.Atoi(strings.TrimPrefix(s.URL, "http://127.0.0.1:"))
	require.NoError(t, err)
	require.NotEmpty(t, wantPort)
	assert.Equal(t, float64(wantPort), s1.Tag(ext.NetworkDestinationPort))
}

func makeRequests(rt http.RoundTripper, url string, t *testing.T) {
	client := &http.Client{
		Transport: rt,
	}
	resp, err := client.Get(url + "/400")
	assert.Nil(t, err)
	defer resp.Body.Close()

	resp, err = client.Get(url + "/500")
	assert.Nil(t, err)
	defer resp.Body.Close()

	resp, err = client.Get(url + "/200")
	assert.Nil(t, err)
	defer resp.Body.Close()
}

func TestRoundTripperErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/200", handler200)
	mux.HandleFunc("/400", handler400)
	mux.HandleFunc("/500", handler500)
	s := httptest.NewServer(mux)
	defer s.Close()

	t.Run("default", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		rt := WrapRoundTripper(http.DefaultTransport)
		makeRequests(rt, s.URL, t)
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 3)
		s := spans[0] // 400 is error
		assert.Equal(t, "400: Bad Request", s.Tag(ext.ErrorMsg))
		assert.Equal(t, "400", s.Tag(ext.HTTPCode))
		s = spans[1] // 500 is not error
		assert.Empty(t, s.Tag(ext.ErrorMsg))
		assert.Equal(t, "500", s.Tag(ext.HTTPCode))
		s = spans[2] // 200 is not error
		assert.Empty(t, s.Tag(ext.ErrorMsg))
		assert.Equal(t, "200", s.Tag(ext.HTTPCode))
	})
	t.Run("custom", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_CLIENT_ERROR_STATUSES", "500-510")
		mt := mocktracer.Start()
		defer mt.Stop()
		rt := WrapRoundTripper(http.DefaultTransport)
		makeRequests(rt, s.URL, t)
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 3)
		s := spans[0] // 400 is not error
		assert.Empty(t, s.Tag(ext.ErrorMsg))
		assert.Equal(t, "400", s.Tag(ext.HTTPCode))
		s = spans[1] // 500 is error
		assert.Equal(t, "500: Internal Server Error", s.Tag(ext.ErrorMsg))
		assert.Equal(t, "500", s.Tag(ext.HTTPCode))
		s = spans[2] // 200 is not error
		assert.Empty(t, s.Tag(ext.ErrorMsg))
		assert.Equal(t, "200", s.Tag(ext.HTTPCode))
	})
}

func TestRoundTripperNetworkError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	done := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header))
		assert.NoError(t, err)
		<-done
	}))
	defer s.Close()
	defer close(done)

	rt := WrapRoundTripper(http.DefaultTransport,
		WithBefore(func(_ *http.Request, span *tracer.Span) {
			span.SetTag("CalledBefore", true)
		}),
		WithAfter(func(_ *http.Response, span *tracer.Span) {
			span.SetTag("CalledAfter", true)
		}))

	client := &http.Client{
		Transport: rt,
		Timeout:   1 * time.Millisecond,
	}

	_, err := client.Get(s.URL + "/hello/world") //nolint:bodyclose
	assert.NotNil(t, err)

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)

	s0 := spans[0]
	assert.Equal(t, "http.request", s0.OperationName())
	assert.Equal(t, "http.request", s0.Tag(ext.ResourceName))
	assert.Equal(t, nil, s0.Tag(ext.HTTPCode))
	assert.Equal(t, "GET", s0.Tag(ext.HTTPMethod))
	assert.Equal(t, s.URL+"/hello/world", s0.Tag(ext.HTTPURL))
	assert.NotNil(t, s0.Tag(ext.ErrorMsg))
	assert.Equal(t, "true", s0.Tag("CalledBefore"))
	assert.Equal(t, "true", s0.Tag("CalledAfter"))
	assert.Equal(t, ext.SpanKindClient, s0.Tag(ext.SpanKind))
	assert.Equal(t, "net/http", s0.Tag(ext.Component))
}

func TestRoundTripperNetworkErrorWithErrorCheck(t *testing.T) {
	failedRequest := func(t *testing.T, mt mocktracer.Tracer, forwardErr bool, _ ...Option) *mocktracer.Span {
		done := make(chan struct{})
		s := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header))
			assert.NoError(t, err)
			<-done
		}))
		defer s.Close()
		defer close(done)

		rt := WrapRoundTripper(http.DefaultTransport,
			WithErrorCheck(func(_ error) bool {
				return forwardErr
			}))

		client := &http.Client{
			Transport: rt,
			Timeout:   1 * time.Millisecond,
		}

		_, err := client.Get(s.URL + "/hello/world") //nolint:bodyclose
		assert.NotNil(t, err)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)

		s0 := spans[0]
		return s0
	}

	t.Run("error skipped", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span := failedRequest(t, mt, false)
		assert.Nil(t, span.Tag(ext.ErrorMsg))
	})

	t.Run("error forwarded", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		span := failedRequest(t, mt, true)
		assert.NotNil(t, span.Tag(ext.ErrorMsg))
	})
}

func TestRoundTripperCredentials(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	var auth string
	s := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if enc, ok := r.Header["Authorization"]; ok {
			encoded := strings.TrimPrefix(enc[0], "Basic ")
			if b64, err := base64.StdEncoding.DecodeString(encoded); err == nil {
				auth = string(b64)
			}
		}

	}))
	defer s.Close()

	rt := WrapRoundTripper(http.DefaultTransport,
		WithBefore(func(_ *http.Request, span *tracer.Span) {
			span.SetTag("CalledBefore", true)
		}),
		WithAfter(func(_ *http.Response, span *tracer.Span) {
			span.SetTag("CalledAfter", true)
		}))

	client := &http.Client{
		Transport: rt,
	}

	u, err := url.Parse(s.URL)
	require.NoError(t, err)
	u.User = url.UserPassword("myuser", "mypassword")

	resp, err := client.Get(u.String() + "/hello/world")
	assert.Nil(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	s1 := spans[0]

	assert.Equal(t, s.URL+"/hello/world", s1.Tag(ext.HTTPURL))
	assert.NotContains(t, s1.Tag(ext.HTTPURL), "mypassword")
	assert.NotContains(t, s1.Tag(ext.HTTPURL), "myuser")
	// Make sure we haven't modified the outgoing request, and the server still
	// receives the auth request.
	assert.Equal(t, auth, "myuser:mypassword")
}

func TestWrapClient(t *testing.T) {
	c := WrapClient(http.DefaultClient)
	assert.Equal(t, c, http.DefaultClient)
	_, ok := c.Transport.(*roundTripper)
	assert.True(t, ok)
}

func TestRoundTripperAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...RoundTripperOption) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		rt := WrapRoundTripper(http.DefaultTransport, opts...)

		client := &http.Client{Transport: rt}
		resp, err := client.Get(srv.URL + "/hello/world")
		assert.Nil(t, err)
		defer resp.Body.Close()
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

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		testutils.SetGlobalAnalyticsRate(t, 0.4)

		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
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
	resp, err := rt.RoundTrip(req)
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Len(t, req.Header, 0)
	assert.Equal(t, initialReq, req)
}

func TestRoundTripperIgnoreRequest(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("Hello World"))
	}))
	defer s.Close()

	rt := WrapRoundTripper(http.DefaultTransport, WithIgnoreRequest(
		func(req *http.Request) bool {
			return req.URL.Path == "/ignore"
		},
	)).(*roundTripper)

	ignoreReq, err := http.NewRequest("GET", s.URL+"/ignore", nil)
	assert.NoError(t, err)
	resp1, err := rt.RoundTrip(ignoreReq)
	assert.NoError(t, err)
	defer resp1.Body.Close()

	req, err := http.NewRequest("GET", s.URL+"/hello", nil)
	assert.NoError(t, err)
	resp2, err := rt.RoundTrip(req)
	assert.NoError(t, err)
	defer resp2.Body.Close()

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
}

func TestRoundTripperStatusCheck(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/not-found" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.WriteHeader(http.StatusTeapot)
	}))
	defer s.Close()

	rt := WrapRoundTripper(http.DefaultTransport, WithStatusCheck(func(statusCode int) bool {
		return statusCode >= 400 && statusCode != http.StatusNotFound
	}))

	client := &http.Client{
		Transport: rt,
	}

	// First request is not marked as an error as it's a 404
	resp, err := client.Get(s.URL + "/not-found")
	assert.Nil(t, err)
	resp.Body.Close()

	spans := mt.FinishedSpans()
	mt.Reset()
	assert.Len(t, spans, 1)
	assert.Equal(t, "http.request", spans[0].OperationName())
	assert.Equal(t, "http.request", spans[0].Tag(ext.ResourceName))
	assert.Equal(t, "404", spans[0].Tag(ext.HTTPCode))
	assert.Equal(t, "GET", spans[0].Tag(ext.HTTPMethod))
	assert.Nil(t, spans[0].Tag("http.errors"))
	assert.Nil(t, spans[0].Tag(ext.ErrorNoStackTrace))

	// Second request is marked as an error as it's a 418
	resp, err = client.Get(s.URL + "/hello/world")
	assert.Nil(t, err)
	resp.Body.Close()

	spans = mt.FinishedSpans()
	assert.Len(t, spans, 1)
	assert.Equal(t, "http.request", spans[0].OperationName())
	assert.Equal(t, "http.request", spans[0].Tag(ext.ResourceName))
	assert.Equal(t, "418", spans[0].Tag(ext.HTTPCode))
	assert.Equal(t, "GET", spans[0].Tag(ext.HTTPMethod))
	assert.EqualValues(t, "418 I'm a teapot", spans[0].Tag("http.errors"))
	assert.EqualValues(t, "418: I'm a teapot", spans[0].Tag(ext.ErrorMsg))
}

func TestRoundTripperURLWithoutPort(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	client := &http.Client{
		Transport: WrapRoundTripper(http.DefaultTransport),
		Timeout:   1 * time.Millisecond,
	}
	_, err := client.Get("http://localhost/hello/world") //nolint:bodyclose
	require.Error(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	s0 := spans[0]
	assert.Equal(t, "http.request", s0.OperationName())
	assert.Equal(t, "http.request", s0.Tag(ext.ResourceName))
	assert.Equal(t, nil, s0.Tag(ext.HTTPCode))
	assert.Equal(t, "GET", s0.Tag(ext.HTTPMethod))
	assert.Equal(t, "http://localhost/hello/world", s0.Tag(ext.HTTPURL))
	assert.NotNil(t, s0.Tag(ext.ErrorMsg))
	assert.Equal(t, ext.SpanKindClient, s0.Tag(ext.SpanKind))
	assert.Equal(t, "net/http", s0.Tag(ext.Component))
	assert.Equal(t, "localhost", s0.Tag(ext.NetworkDestinationName))
	assert.NotContains(t, s0.Tags(), ext.NetworkDestinationPort)
}

func TestServiceName(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("Hello World"))
	}))
	defer s.Close()

	t.Run("option", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		serviceName := "testServer"
		rt := WrapRoundTripper(http.DefaultTransport, WithService(serviceName))
		client := &http.Client{
			Transport: rt,
		}
		resp, err := client.Get(s.URL + "/hello/world")
		assert.Nil(t, err)
		defer resp.Body.Close()
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		assert.Equal(t, serviceName, spans[0].Tag(ext.ServiceName))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		serviceName := "testServer"
		rt := WrapRoundTripper(http.DefaultTransport,
			WithService("wrongServiceName"),
			WithBefore(func(_ *http.Request, span *tracer.Span) {
				span.SetTag(ext.ServiceName, serviceName)
			}),
		)
		client := &http.Client{
			Transport: rt,
		}
		resp, err := client.Get(s.URL + "/hello/world")
		assert.Nil(t, err)
		defer resp.Body.Close()
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		assert.Equal(t, serviceName, spans[0].Tag(ext.ServiceName))
	})
}

func TestRoundTripperOTelSemanticConfig(t *testing.T) {
	setOTelSemantics(t, "true")

	t.Run("defaults", func(t *testing.T) {
		cfg := newRoundTripperConfig()
		require.True(t, cfg.OTelSemanticsEnabled)
		assert.True(t, cfg.IsStatusError(500))
		assert.Equal(t, "GET", cfg.ResourceNamer(httptest.NewRequest(http.MethodGet, "http://example.com/path", nil)))
		assert.Equal(t, "HTTP", cfg.ResourceNamer(httptest.NewRequest("PROPFIND", "http://example.com/path", nil)))
	})

	t.Run("custom resource namer", func(t *testing.T) {
		cfg := newRoundTripperConfig()
		cfg.ApplyOpts(WithResourceNamer(func(*http.Request) string { return "custom" }))
		assert.Equal(t, "custom", cfg.ResourceNamer(httptest.NewRequest(http.MethodGet, "http://example.com/path", nil)))
	})

}

func TestRoundTripperLegacyConfig(t *testing.T) {
	setOTelSemantics(t, "false")
	cfg := newRoundTripperConfig()
	assert.False(t, cfg.OTelSemanticsEnabled)
	assert.False(t, cfg.IsStatusError(500))
	assert.Equal(t, "http.request", cfg.ResourceNamer(httptest.NewRequest(http.MethodGet, "http://example.com/path", nil)))
}

func TestRoundTripperOTelSemanticsDisabledByDefault(t *testing.T) {
	setOTelSemantics(t, "")
	assert.False(t, newRoundTripperConfig().OTelSemanticsEnabled)
}

func TestRoundTripperOTelSemanticRequest(t *testing.T) {
	setOTelSemantics(t, "true")
	req, err := http.NewRequest("PROPFIND", "http://alice:secret@example.com:8080/path?something=fun#fragment", nil)
	require.NoError(t, err)
	originalURL := req.URL.String()
	originalUser := req.URL.User

	span, _ := roundTripSpan(t, &emptyRoundTripper{}, req)
	assert.Equal(t, "HTTP", span.Tag(ext.ResourceName))
	assert.Equal(t, "_OTHER", span.Tag("http.request.method"))
	assert.Equal(t, "PROPFIND", span.Tag("http.request.method_original"))
	assert.Equal(t, "http://REDACTED:REDACTED@example.com:8080/path?something=fun#fragment", span.Tag("url.full"))
	assert.Equal(t, "example.com", span.Tag("server.address"))
	assert.Equal(t, float64(8080), span.Tag("server.port"))
	assert.Equal(t, ext.SpanKindClient, span.Tag(ext.SpanKind))
	assert.Equal(t, "net/http", span.Tag(ext.Component))

	for _, legacyTag := range []string{
		ext.HTTPMethod,
		ext.HTTPURL,
		"out.host",
		"out.port",
		ext.PeerHostname,
		ext.NetworkDestinationName,
		ext.NetworkDestinationPort,
		"url.path",
		"url.scheme",
		"url.query",
	} {
		assert.NotContains(t, span.Tags(), legacyTag)
	}
	assert.Equal(t, originalURL, req.URL.String())
	assert.Same(t, originalUser, req.URL.User)
}

func TestRoundTripperOTelSemanticRequestOptions(t *testing.T) {
	setOTelSemantics(t, "true")

	t.Run("query disabled", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_CLIENT_TAG_QUERY_STRING", "false")
		span, _ := roundTripSpan(t, &emptyRoundTripper{}, httptest.NewRequest(http.MethodGet, "http://example.com/path?something=fun", nil))
		assert.Equal(t, "http://example.com/path", span.Tag("url.full"))
		assert.Equal(t, "GET", span.Tag(ext.ResourceName))
		assert.Nil(t, span.Tag("http.request.method_original"))
	})

	t.Run("empty method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/path", nil)
		req.Method = ""

		span, _ := roundTripSpan(t, &emptyRoundTripper{}, req)
		assert.Equal(t, "GET", span.Tag(ext.HTTPRequestMethod))
		assert.Equal(t, "GET", span.Tag(ext.ResourceName))
		assert.Nil(t, span.Tag(ext.HTTPRequestMethodOriginal))
		assert.Empty(t, req.Method)
	})

	t.Run("Host override", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "https://origin.example:8443/path?keep=value", nil)
		req.Host = "override.example:9443"

		span, _ := roundTripSpan(t, &emptyRoundTripper{}, req)
		assert.Equal(t, "https://origin.example:8443/path?keep=value", span.Tag(ext.URLFull))
		assert.Equal(t, "override.example", span.Tag(ext.ServerAddress))
		assert.Equal(t, float64(9443), span.Tag(ext.ServerPort))
	})

	t.Run("recognized QUERY method", func(t *testing.T) {
		span, _ := roundTripSpan(t, &emptyRoundTripper{}, httptest.NewRequest("QUERY", "http://example.com/users/123", nil))
		assert.Equal(t, "QUERY", span.Tag("http.request.method"))
		assert.Equal(t, "QUERY", span.Tag(ext.ResourceName))
		assert.Nil(t, span.Tag("http.request.method_original"))
	})

	t.Run("custom resource namer", func(t *testing.T) {
		span, _ := roundTripSpan(
			t,
			&emptyRoundTripper{},
			httptest.NewRequest(http.MethodGet, "http://example.com/users/123", nil),
			WithResourceNamer(func(*http.Request) string { return "custom" }),
		)
		assert.Equal(t, "custom", span.Tag(ext.ResourceName))
	})

	t.Run("default query obfuscation", func(t *testing.T) {
		span, _ := roundTripSpan(t, &emptyRoundTripper{}, httptest.NewRequest(http.MethodGet, "http://example.com/path?token=secret&keep=value", nil))
		assert.Equal(t, "http://example.com/path?<redacted>&keep=value", span.Tag("url.full"))
	})

	t.Run("caller span option overrides default", func(t *testing.T) {
		span, _ := roundTripSpan(
			t,
			&emptyRoundTripper{},
			httptest.NewRequest(http.MethodGet, "http://example.com/path", nil),
			WithSpanOptions(tracer.Tag("server.address", "override.example")),
		)
		assert.Equal(t, "override.example", span.Tag("server.address"))
	})
}

func TestRoundTripperOTelSemanticResponseStatus(t *testing.T) {
	setOTelSemantics(t, "true")

	tests := []struct {
		name        string
		status      int
		customCheck bool
		wantError   bool
	}{
		{name: "success", status: http.StatusOK},
		{name: "server error", status: http.StatusInternalServerError, wantError: true},
		{name: "uninterpretable status", status: 600, wantError: true},
		{name: "custom check includes success", status: http.StatusOK, customCheck: true, wantError: true},
		{name: "custom check excludes server error", status: http.StatusInternalServerError, customCheck: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			var opts []RoundTripperOption
			if tt.customCheck {
				opts = append(opts, WithStatusCheck(func(status int) bool {
					calls++
					assert.Equal(t, tt.status, status)
					return tt.wantError
				}))
			}
			recorder := httptest.NewRecorder()
			recorder.WriteHeader(tt.status)
			span, resp := roundTripSpan(t,
				&emptyRoundTripper{customResponse: recorder.Result()},
				httptest.NewRequest(http.MethodGet, "http://example.com/path", nil), opts...)
			if tt.customCheck {
				assert.Equal(t, 1, calls)
			}
			statusCode := strconv.Itoa(tt.status)
			assert.Equal(t, statusCode, span.Tag("http.response.status_code"))
			assert.NotContains(t, span.Tags(), ext.HTTPCode)
			assert.Equal(t, tt.wantError, span.Unwrap().AsMap()[ext.MapSpanError] == int32(1))
			if tt.wantError {
				assert.Equal(t, statusCode, span.Tag(ext.ErrorType))
				assert.Equal(t, resp.Status, span.Tag("http.errors"))
				assert.Equal(t, fmt.Sprintf("%d: %s", tt.status, http.StatusText(tt.status)), span.Tag(ext.ErrorMsg))
			} else {
				assert.Nil(t, span.Tag(ext.ErrorType))
				assert.Nil(t, span.Tag(ext.ErrorMsg))
				assert.Nil(t, span.Tag("http.errors"))
			}
		})
	}
}

func TestRoundTripperOTelSemanticTransportError(t *testing.T) {
	setOTelSemantics(t, "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	transportErr := &roundTripperTestError{}
	rt := WrapRoundTripper(&emptyRoundTripper{customError: transportErr})
	resp, err := rt.RoundTrip(httptest.NewRequest(http.MethodGet, "http://example.com/path", nil))
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, transportErr)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.NotContains(t, spans[0].Tags(), ext.HTTPResponseStatusCode)
	assert.NotContains(t, spans[0].Tags(), ext.HTTPCode)
}

func TestResourceNamer(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		resp, err := client.Get(s.URL + "/hello/world")
		assert.Nil(t, err)
		defer resp.Body.Close()
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
		rt := WrapRoundTripper(http.DefaultTransport, WithResourceNamer(customNamer))
		client := &http.Client{
			Transport: rt,
		}
		resp, err := client.Get(s.URL + "/hello/world")
		assert.Nil(t, err)
		defer resp.Body.Close()
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		assert.Equal(t, "GET /hello/world", spans[0].Tag(ext.ResourceName))
	})
}

func TestSpanOptions(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("")) }))
	defer s.Close()

	tagKey := "foo"
	tagValue := "bar"
	mt := mocktracer.Start()
	defer mt.Stop()
	rt := WrapRoundTripper(http.DefaultTransport, WithSpanOptions(tracer.Tag(tagKey, tagValue)))
	client := &http.Client{Transport: rt}

	resp, err := client.Get(s.URL)
	assert.Nil(t, err)
	defer resp.Body.Close()

	spans := mt.FinishedSpans()
	assert.Len(t, spans, 1)
	assert.Equal(t, tagValue, spans[0].Tag(tagKey))
}

func TestClientTimings(t *testing.T) {
	assertClientTimings := func(t *testing.T, enabled bool, expectTags bool) {
		mt := mocktracer.Start()
		defer mt.Stop()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		rt := WrapRoundTripper(http.DefaultTransport, WithClientTimings(enabled))
		client := &http.Client{Transport: rt}
		resp, err := client.Get(srv.URL)
		assert.Nil(t, err)
		defer resp.Body.Close()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		span := spans[0]

		hasTimingTags := span.Tag("http.connect.duration_ms") != nil ||
			span.Tag("http.get_conn.duration_ms") != nil ||
			span.Tag("http.first_byte.duration_ms") != nil

		assert.Equal(t, expectTags, hasTimingTags)
	}

	t.Run("disabled", func(t *testing.T) {
		assertClientTimings(t, false, false)
	})

	t.Run("enabled", func(t *testing.T) {
		assertClientTimings(t, true, true)
	})
}

func TestClientTimingsRace(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := WrapRoundTripper(http.DefaultTransport, WithClientTimings(true))
	client := &http.Client{Transport: rt}

	const numGoroutines = 10
	const numReqs = 10

	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numReqs; j++ {
				resp, err := client.Get(srv.URL)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
}

func TestClientQueryStringCollected(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("Hello World"))
	}))
	defer s.Close()
	t.Run("default true", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rt := WrapRoundTripper(http.DefaultTransport)
		client := &http.Client{
			Transport: rt,
		}
		resp, err := client.Get(s.URL + "/hello/world?something=fun")
		assert.Nil(t, err)
		defer resp.Body.Close()
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)

		assert.Regexp(t, regexp.MustCompile(`^http://.*?/hello/world\?something=fun$`), spans[0].Tag(ext.HTTPURL))
	})
	t.Run("false", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		t.Setenv("DD_TRACE_HTTP_CLIENT_TAG_QUERY_STRING", "false")

		rt := WrapRoundTripper(http.DefaultTransport)
		client := &http.Client{
			Transport: rt,
		}
		resp, err := client.Get(s.URL + "/hello/world?querystring=xyz")
		assert.Nil(t, err)
		defer resp.Body.Close()
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)

		assert.Regexp(t, regexp.MustCompile(`^http://.*?/hello/world$`), spans[0].Tag(ext.HTTPURL))
	})
	// DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED applies only to server spans, not client
	t.Run("Not impacted by DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		t.Setenv("DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED", "false")

		rt := WrapRoundTripper(http.DefaultTransport)
		client := &http.Client{
			Transport: rt,
		}
		resp, err := client.Get(s.URL + "/hello/world?something=fun")
		assert.Nil(t, err)
		defer resp.Body.Close()
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)

		assert.Regexp(t, regexp.MustCompile(`^http://.*?/hello/world\?something=fun$`), spans[0].Tag(ext.HTTPURL))
	})
}

func TestClientQueryStringObfuscated(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		resp, err := client.Get(s.URL + "/hello/world?token=value")
		assert.Nil(t, err)
		defer resp.Body.Close()
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)

		assert.Regexp(t, regexp.MustCompile(`^http://.*?/hello/world\?<redacted>$`), spans[0].Tag(ext.HTTPURL))
	})
	t.Run("empty", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		t.Setenv(internal.EnvQueryStringRegexp, "")

		rt := WrapRoundTripper(http.DefaultTransport)
		client := &http.Client{
			Transport: rt,
		}
		resp, err := client.Get(s.URL + "/hello/world?custom=xyz")
		assert.Nil(t, err)
		defer resp.Body.Close()
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)

		assert.Regexp(t, regexp.MustCompile(`^http://.*?/hello/world\?custom=xyz$`), spans[0].Tag(ext.HTTPURL))
	})
	t.Run("custom", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		t.Setenv(internal.EnvQueryStringRegexp, "^custom")

		rt := WrapRoundTripper(http.DefaultTransport)
		client := &http.Client{
			Transport: rt,
		}
		resp, err := client.Get(s.URL + "/hello/world?token=value")
		assert.Nil(t, err)
		defer resp.Body.Close()
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)

		assert.Regexp(t, regexp.MustCompile(`^http://.*?/hello/world\?<redacted>$`), spans[0].Tag(ext.HTTPURL))
	})
}

func TestRoundTripperPropagation(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		spanctx, err := tracer.Extract(tracer.HTTPHeadersCarrier(r.Header))
		assert.ErrorIs(t, err, tracer.ErrSpanContextNotFound, "should not find headers injected in output")

		assert.Empty(t, r.Header.Get(tracer.DefaultTraceIDHeader), "should not find trace_id in output header")
		assert.Empty(t, r.Header.Get(tracer.DefaultParentIDHeader), "should not find parent_id in output header")

		span := tracer.StartSpan("test",
			tracer.ChildOf(spanctx))
		defer span.Finish()

		w.Write([]byte("Hello World"))
	}))
	defer s.Close()

	rt := WrapRoundTripper(http.DefaultTransport,
		WithPropagation(false))
	client := &http.Client{
		Transport: rt,
	}

	resp, err := client.Get(s.URL + "/hello/world")
	assert.Nil(t, err)
	defer resp.Body.Close()
}

type roundTripperTestError struct{}

func (*roundTripperTestError) Error() string { return "transport failed" }

type emptyRoundTripper struct {
	customResponse *http.Response
	customError    error
}

func (rt *emptyRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	if rt.customError != nil {
		return nil, rt.customError
	}
	if rt.customResponse != nil {
		return rt.customResponse, nil
	}

	recorder := httptest.NewRecorder()
	recorder.WriteHeader(200)
	return recorder.Result(), nil
}

func BenchmarkRoundTripperOTelSemantics(b *testing.B) {
	require.NoError(b, tracer.Start(tracer.WithTraceEnabled(false)))
	b.Cleanup(tracer.Stop)

	for _, enabled := range []bool{false, true} {
		b.Run(strconv.FormatBool(enabled), func(b *testing.B) {
			cfg := newRoundTripperConfig()
			cfg.OTelSemanticsEnabled = enabled
			cfg.ResourceNamer = func(*http.Request) string { return "GET" }
			rt := &roundTripper{base: &emptyRoundTripper{}, cfg: cfg}
			req := httptest.NewRequest(http.MethodGet, "http://example.com:8080/path?keep=value", nil)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp, err := rt.RoundTrip(req)
				if err != nil {
					b.Fatal(err)
				}
				if err := resp.Body.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestRoundTripperWithBaggage(t *testing.T) {
	t.Setenv("DD_TRACE_PROPAGATION_STYLE", "datadog,tracecontext,baggage")
	tracer.Start()
	defer tracer.Stop()

	var capturedHeaders http.Header

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Hello with Baggage!"))
	}))
	defer s.Close()

	rt := WrapRoundTripper(http.DefaultTransport).(*roundTripper)

	ctx := context.Background()
	ctx = baggage.Set(ctx, "foo", "bar")
	ctx = baggage.Set(ctx, "baz", "qux")

	// Build the HTTP request with that context.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/baggage", nil)
	assert.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.NotEmpty(t, capturedHeaders.Get("baggage"), "should have baggage header")
}

// hasControlByte reports whether s contains a raw CR, LF, or NUL byte -- the
// bytes an attacker needs to smuggle extra header lines into a carrier that
// writes header values without validation.
func hasControlByte(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\r', '\n', 0x00:
			return true
		}
	}
	return false
}

// TestBaggageControlCharsNotInjectedOnOutboundHTTP reproduces the end-to-end
// impact of a poisoned baggage value: "v\r\nX-Evil:1" is exactly what an
// upstream "baggage: k=v%0D%0AX-Evil:1" header decodes to. Once that value is
// set as request baggage, the wrapped client's outbound call must not be
// rejected by net/http, and the downstream server must not see a raw CR/LF/
// NUL in the injected ot-baggage-* header.
func TestBaggageControlCharsNotInjectedOnOutboundHTTP(t *testing.T) {
	t.Setenv("DD_TRACE_PROPAGATION_STYLE", "datadog,tracecontext,baggage")
	tracer.Start()
	defer tracer.Stop()

	var capturedHeaders http.Header
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer s.Close()

	rt := WrapRoundTripper(http.DefaultTransport).(*roundTripper)

	ctx := baggage.Set(context.Background(), "k", "v\r\nX-Evil:1")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req) //nolint:bodyclose
	require.NoError(t, err, "outbound call must not be rejected because of a poisoned ot-baggage-* header")
	defer resp.Body.Close()

	found := false
	for k, vals := range capturedHeaders {
		if !strings.HasPrefix(strings.ToLower(k), tracer.DefaultBaggageHeaderPrefix) {
			continue
		}
		found = true
		for _, v := range vals {
			assert.False(t, hasControlByte(v), "%s must not carry a raw control byte, got %q", k, v)
		}
	}
	assert.True(t, found, "expected an ot-baggage-* header on the outbound request")
}
