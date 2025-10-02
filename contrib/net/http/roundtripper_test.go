// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internal "github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
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

type emptyRoundTripper struct {
	customResponse *http.Response
}

func (rt *emptyRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	if rt.customResponse != nil {
		return rt.customResponse, nil
	}

	recorder := httptest.NewRecorder()
	recorder.WriteHeader(200)
	return recorder.Result(), nil
}

func TestAppsec(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/rasp.json")

	client := WrapRoundTripper(&emptyRoundTripper{})

	for _, enabled := range []bool{true, false} {

		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			t.Setenv("DD_APPSEC_RASP_ENABLED", strconv.FormatBool(enabled))

			mt := mocktracer.Start()
			defer mt.Stop()

			testutils.StartAppSec(t)
			if !internal.Instrumentation.AppSecEnabled() {
				t.Skip("appsec not enabled")
			}

			w := httptest.NewRecorder()
			r, err := http.NewRequest("GET", "?value=169.254.169.254", nil)
			require.NoError(t, err)

			TraceAndServe(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				req, err := http.NewRequestWithContext(r.Context(), "GET", "http://169.254.169.254", nil)
				require.NoError(t, err)

				resp, err := client.RoundTrip(req)

				if enabled {
					require.True(t, events.IsSecurityError(err))
				} else {
					require.NoError(t, err)
				}

				if resp != nil {
					defer resp.Body.Close()
				}
			}), w, r, &ServeConfig{
				Service:  "service",
				Resource: "resource",
			})

			spans := mt.FinishedSpans()
			require.Len(t, spans, 2) // service entry serviceSpan & http request serviceSpan
			serviceSpan := spans[1]

			if !enabled {
				require.NotContains(t, serviceSpan.Tags(), "_dd.appsec.json")
				require.NotContains(t, serviceSpan.Tags(), "_dd.stack")
				return
			}

			require.Contains(t, serviceSpan.Tags(), "_dd.appsec.json")
			appsecJSON := serviceSpan.Tag("_dd.appsec.json")
			require.Contains(t, appsecJSON, addresses.ServerIONetURLAddr)

			require.Contains(t, serviceSpan.Tags(), "_dd.stack")
			require.NotContains(t, serviceSpan.Tags(), "error.message")

			// This is a nested event so it should contain the child span id in the service entry span
			// TODO(eliott.bouhana): uncomment this once we have the child span id in the service entry span
			// require.Contains(t, appsecJSON, `"span_id":`+strconv.FormatUint(requestSpan.SpanID(), 10))
		})
	}
}

func TestAppsecAPI10(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/api10.json")
	t.Setenv("DD_API_SECURITY_DOWNSTREAM_REQUEST_BODY_ANALYSIS_SAMPLE_RATE", "1.0")

	var b strings.Builder
	b.WriteString(`{"payload_in":"%s"`)
	for i := 0; i < 1<<12; i++ {
		b.WriteString(fmt.Sprintf(`,"%d":"b"`, i))
	}
	b.WriteString(`}`)

	for _, tc := range []struct {
		name     string
		request  func(ctx context.Context) *http.Request
		response *http.Response
		tagName  string
		tagValue string
	}{
		{
			name: "method",
			request: func(ctx context.Context) *http.Request {
				req, _ := http.NewRequestWithContext(ctx, "TRACE", "http://localhost:8080", nil)
				return req
			},
			tagName:  "_dd.appsec.trace.req_method",
			tagValue: "TAG_API10_REQ_METHOD",
		},
		{
			name: "headers",
			request: func(ctx context.Context) *http.Request {
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", nil)
				req.Header.Set("Witness", "pwq3ojtropiw3hjtowir")
				return req
			},
			tagName:  "_dd.appsec.trace.req_headers",
			tagValue: "TAG_API10_REQ_HEADERS",
		},
		{
			name: "body",
			request: func(ctx context.Context) *http.Request {
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", io.NopCloser(strings.NewReader(`{"payload_in":"qw2jedrkjerbgol23ewpfirj2qw3or"}`)))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
			tagName:  "_dd.appsec.trace.req_body",
			tagValue: "TAG_API10_REQ_BODY",
		},
		{
			name: "big-body",
			request: func(ctx context.Context) *http.Request {
				t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
				body := fmt.Sprintf(b.String(), "qw2jedrkjerbgol23ewpfirj2qw3or")
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", io.NopCloser(strings.NewReader(body)))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
			tagName:  "_dd.appsec.trace.req_body",
			tagValue: "TAG_API10_REQ_BODY",
		},
		{
			name: "resp-status",
			request: func(ctx context.Context) *http.Request {
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", nil)
				return req
			},
			response: &http.Response{
				StatusCode: 201,
			},
			tagName:  "_dd.appsec.trace.res_status",
			tagValue: "TAG_API10_RES_STATUS",
		},
		{
			name: "resp-headers",
			request: func(ctx context.Context) *http.Request {
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", nil)
				return req
			},
			response: &http.Response{
				StatusCode: 200,
				Header: map[string][]string{
					"echo-headers": {"qwoierj12l3"},
				},
			},
			tagName:  "_dd.appsec.trace.res_headers",
			tagValue: "TAG_API10_RES_HEADERS",
		},
		{
			name: "resp-body",
			request: func(ctx context.Context) *http.Request {
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", nil)
				return req
			},
			response: &http.Response{
				StatusCode: 200,
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
				Body: io.NopCloser(strings.NewReader(`{"payload_out":"kqehf09123r4lnksef"}`)),
			},
			tagName:  "_dd.appsec.trace.res_body",
			tagValue: "TAG_API10_RES_BODY",
		},
		{
			name: "resp-big-body",
			request: func(ctx context.Context) *http.Request {
				t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080", nil)
				return req
			},
			response: &http.Response{
				StatusCode: 200,
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
				Body: io.NopCloser(strings.NewReader(fmt.Sprintf(b.String(), "kqehf09123r4lnksef"))),
			},
			tagName:  "_dd.appsec.trace.res_body",
			tagValue: "TAG_API10_RES_BODY",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {

			client := WrapRoundTripper(&emptyRoundTripper{customResponse: tc.response})

			mt := mocktracer.Start()
			defer mt.Stop()

			testutils.StartAppSec(t)
			if !internal.Instrumentation.AppSecEnabled() {
				t.Skip("appsec not enabled")
			}

			w := httptest.NewRecorder()
			r, err := http.NewRequest("GET", "", nil)
			require.NoError(t, err)

			TraceAndServe(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				resp, err := client.RoundTrip(tc.request(r.Context()))
				require.NoError(t, err)
				if resp != nil && resp.Body != nil {
					defer resp.Body.Close()
				}
			}), w, r, &ServeConfig{
				Service:  "service",
				Resource: "resource",
			})

			spans := mt.FinishedSpans()
			require.Len(t, spans, 2) // service entry serviceSpan & http request serviceSpan
			serviceSpan := spans[1]

			require.Contains(t, serviceSpan.Tags(), tc.tagName)
			require.Equal(t, serviceSpan.Tags()[tc.tagName], tc.tagValue)
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
