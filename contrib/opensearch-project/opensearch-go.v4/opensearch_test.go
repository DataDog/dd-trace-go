// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package opensearch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func newTestServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPerform(t *testing.T) {
	testutils.SetGlobalServiceName(t, "global-service")

	tests := []struct {
		name       string
		opts       []Option
		status     int
		respBody   string
		method     string
		path       string
		reqBody    string
		assertSpan func(t *testing.T, span *mocktracer.Span)
	}{
		{
			name:     "defaults",
			status:   http.StatusOK,
			respBody: `{}`,
			method:   http.MethodPut,
			path:     "/my-index-2024/_doc/123?refresh=true",
			reqBody:  `{"title":"some-title"}`,
			assertSpan: func(t *testing.T, span *mocktracer.Span) {
				assert.Equal(t, "opensearch.query", span.OperationName())
				assert.Equal(t, "global-service", span.Tag(ext.ServiceName))
				// The tracer omits the source when the service is the global one.
				assert.Nil(t, span.Tag(ext.KeyServiceSource))
				assert.Equal(t, "PUT /my-index-?/_doc/?", span.Tag(ext.ResourceName))
				assert.Equal(t, http.MethodPut, span.Tag(ext.OpenSearchMethod))
				assert.Equal(t, "/my-index-2024/_doc/123", span.Tag(ext.OpenSearchURL))
				assert.Equal(t, "refresh=true", span.Tag(ext.OpenSearchParams))
				assert.Equal(t, `{"title":"some-title"}`, span.Tag(ext.OpenSearchBody))
				assert.Equal(t, "200", span.Tag(ext.HTTPCode))
				assert.Nil(t, span.Tag(ext.ErrorMsg))
			},
		},
		{
			name: "options",
			opts: []Option{
				WithService("custom-service"),
				WithResourceNamer(func(_, _ string) string { return "custom-resource" }),
				WithCustomTag("custom.tag", "custom-value"),
			},
			status:   http.StatusOK,
			respBody: `{}`,
			method:   http.MethodGet,
			path:     "/_cluster/health",
			assertSpan: func(t *testing.T, span *mocktracer.Span) {
				assert.Equal(t, "custom-service", span.Tag(ext.ServiceName))
				assert.Equal(t, instrumentation.ServiceSourceWithServiceOption, span.Tag(ext.KeyServiceSource))
				assert.Equal(t, "custom-resource", span.Tag(ext.ResourceName))
				assert.Equal(t, "custom-value", span.Tag("custom.tag"))
			},
		},
		{
			name:     "error status",
			status:   http.StatusNotFound,
			respBody: `{"error":"index_not_found_exception"}`,
			method:   http.MethodGet,
			path:     "/missing-index",
			assertSpan: func(t *testing.T, span *mocktracer.Span) {
				assert.Equal(t, "404", span.Tag(ext.HTTPCode))
				assert.Equal(t, `{"error":"index_not_found_exception"}`, span.Tag(ext.ErrorMsg))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			srv := newTestServer(t, tt.status, tt.respBody)
			client, err := NewClient(opensearch.Config{Addresses: []string{srv.URL}}, tt.opts...)
			require.NoError(t, err)

			var body io.Reader
			if tt.reqBody != "" {
				body = strings.NewReader(tt.reqBody)
			}
			req, err := http.NewRequestWithContext(context.Background(), tt.method, tt.path, body)
			require.NoError(t, err)
			resp, err := client.Perform(req)
			require.NoError(t, err)
			respBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			resp.Body.Close()
			assert.Equal(t, tt.respBody, string(respBody), "the response body should be readable after tracing")

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			span := spans[0]
			srvURL, err := url.Parse(srv.URL)
			require.NoError(t, err)
			assert.Equal(t, string(instrumentation.PackageOpenSearchProjectOpenSearchGoV4), span.Tag(ext.Component))
			assert.Equal(t, string(instrumentation.PackageOpenSearchProjectOpenSearchGoV4), span.Integration())
			assert.Equal(t, ext.SpanKindClient, span.Tag(ext.SpanKind))
			assert.Equal(t, ext.DBSystemOpenSearch, span.Tag(ext.DBSystem))
			assert.Equal(t, ext.SpanTypeOpenSearch, span.Tag(ext.SpanType))
			assert.Equal(t, srvURL.Hostname(), span.Tag(ext.NetworkDestinationName))
			assert.Equal(t, srvURL.Hostname(), span.Tag(ext.TargetHost))
			assert.Equal(t, srvURL.Port(), span.Tag(ext.TargetPort))
			tt.assertSpan(t, span)
		})
	}
}

func TestPerformTransportError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// A listener that is closed right away gives an address nothing listens on.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	client, err := NewClient(opensearch.Config{
		Addresses:    []string{"http://" + addr},
		DisableRetry: true,
	})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	require.NoError(t, err)
	resp, err := client.Perform(req)
	if resp != nil {
		resp.Body.Close()
	}
	require.Error(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.NotNil(t, spans[0].Tag(ext.ErrorMsg))
}

type fakeTransport struct{}

func (fakeTransport) Perform(*http.Request) (*http.Response, error) { return nil, nil }

func TestTraceClient(t *testing.T) {
	t.Run("delegates to the original transport", func(t *testing.T) {
		client, err := opensearch.NewClient(opensearch.Config{Addresses: []string{"http://127.0.0.1:9200"}})
		require.NoError(t, err)
		TraceClient(client)
		require.IsType(t, &transport{}, client.Transport)

		_, err = client.Metrics()
		assert.NotErrorIs(t, err, opensearch.ErrTransportMissingMethodMetrics)
	})
	t.Run("missing methods", func(t *testing.T) {
		client := &opensearch.Client{Transport: fakeTransport{}}
		TraceClient(client)

		_, err := client.Transport.(opensearchtransport.Measurable).Metrics()
		assert.ErrorIs(t, err, opensearch.ErrTransportMissingMethodMetrics)
		err = client.Transport.(opensearchtransport.Discoverable).DiscoverNodes(context.Background())
		assert.ErrorIs(t, err, opensearch.ErrTransportMissingMethodDiscoverNodes)
	})
}

func TestQuantize(t *testing.T) {
	for _, tt := range []struct {
		url, method, want string
	}{
		{url: "/_search", method: "GET", want: "GET /_search"},
		{url: "/twitter/tweets", method: "POST", want: "POST /twitter/tweets"},
		{url: "/logs_2016_05/event/_search", method: "GET", want: "GET /logs_?_?/event/_search"},
		{url: "/twitter/tweets/123", method: "GET", want: "GET /twitter/tweets/?"},
		{url: "/logs_2016_05/event/123", method: "PUT", want: "PUT /logs_?_?/event/?"},
	} {
		t.Run(tt.method+" "+tt.url, func(t *testing.T) {
			assert.Equal(t, tt.want, quantize(tt.url, tt.method))
		})
	}
}

func TestPeek(t *testing.T) {
	gzipped := func(s string) io.ReadCloser {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		_, _ = w.Write([]byte(s))
		_ = w.Close()
		return io.NopCloser(&buf)
	}
	for _, tt := range []struct {
		name     string
		rc       io.ReadCloser
		encoding string
		maxLen   int
		n        int
		want     string
		wantErr  bool
	}{
		{name: "nil stream", rc: nil, n: 10, wantErr: true},
		{name: "shorter than n", rc: io.NopCloser(strings.NewReader("abc")), maxLen: -1, n: 10, want: "abc"},
		{name: "cut to n", rc: io.NopCloser(strings.NewReader("abcdef")), maxLen: -1, n: 3, want: "abc"},
		{name: "cut to max length", rc: io.NopCloser(strings.NewReader("abcdef")), maxLen: 2, n: 10, want: "ab"},
		{name: "gzip", rc: gzipped("abcdef"), encoding: "gzip", maxLen: -1, n: 1024, want: "abcdef"},
		{name: "not gzip", rc: io.NopCloser(strings.NewReader("abc")), encoding: "gzip", maxLen: -1, n: 10, want: "abc"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			snip, rc, err := peek(tt.rc, tt.encoding, tt.maxLen, tt.n)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, snip)
			// The returned reader still holds the whole stream.
			_, err = io.ReadAll(rc)
			assert.NoError(t, err)
		})
	}
}

func TestPeekReadError(t *testing.T) {
	errRead := errors.New("read failed")
	_, _, err := peek(io.NopCloser(&errReader{err: errRead}), "", -1, 10)
	assert.ErrorIs(t, err, errRead)
}

type errReader struct{ err error }

func (r *errReader) Read([]byte) (int, error) { return 0, r.err }

func TestNewClientInsecureSkipVerify(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(strconv.FormatBool(custom), func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{}`)
			}))
			defer srv.Close()
			cfg := opensearch.Config{Addresses: []string{srv.URL}, InsecureSkipVerify: true}
			var original *http.Transport
			if custom {
				original = http.DefaultTransport.(*http.Transport).Clone()
				original.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
				cfg.Transport = original
			}
			client, err := NewClient(cfg)
			require.NoError(t, err)
			defer client.Close()
			req, err := http.NewRequest(http.MethodGet, "/", nil)
			require.NoError(t, err)
			resp, err := client.Perform(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			if custom {
				assert.False(t, original.TLSClientConfig.InsecureSkipVerify)
			}
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			u, err := url.Parse(srv.URL)
			require.NoError(t, err)
			assert.Equal(t, u.Hostname(), spans[0].Tag(ext.TargetHost))
			assert.Equal(t, u.Port(), spans[0].Tag(ext.TargetPort))
		})
	}
}

func TestStream(t *testing.T) {
	for _, traced := range []bool{false, true} {
		for _, status := range []int{http.StatusOK, http.StatusNotFound} {
			t.Run(strconv.FormatBool(traced)+"/"+strconv.Itoa(status), func(t *testing.T) {
				mt := mocktracer.Start()
				defer mt.Stop()
				srv := newTestServer(t, status, `{"result":"streamed"}`)
				cfg := opensearch.Config{Addresses: []string{srv.URL}}
				var client *opensearch.Client
				var err error
				opts := []Option{WithService("stream-service"), WithCustomTag("custom.tag", "value")}
				if traced {
					client, err = opensearch.NewClient(cfg)
					require.NoError(t, err)
					TraceClient(client, opts...)
				} else {
					client, err = NewClient(cfg, opts...)
					require.NoError(t, err)
				}
				defer client.Close()
				parent := mt.StartSpan("parent")
				defer parent.Finish()
				req, err := http.NewRequestWithContext(tracer.ContextWithSpan(context.Background(), parent), http.MethodPost, "/_search?pretty=true", strings.NewReader(`{"query":{}}`))
				require.NoError(t, err)
				resp, err := client.Stream(req)
				require.NoError(t, err)
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Equal(t, `{"result":"streamed"}`, string(body))
				spans := mt.FinishedSpans()
				require.Len(t, spans, 1)
				span := spans[0]
				assert.Equal(t, parent.Context().SpanID(), span.ParentID())
				assert.Equal(t, "opensearch.query", span.OperationName())
				assert.Equal(t, "stream-service", span.Tag(ext.ServiceName))
				assert.Equal(t, "value", span.Tag("custom.tag"))
				assert.Equal(t, "POST /_search", span.Tag(ext.ResourceName))
				assert.Nil(t, span.Tag(ext.OpenSearchBody))
				assert.Equal(t, "pretty=true", span.Tag(ext.OpenSearchParams))
				assert.Equal(t, strconv.Itoa(status), span.Tag(ext.HTTPCode))
				u, err := url.Parse(srv.URL)
				require.NoError(t, err)
				assert.Equal(t, u.Hostname(), span.Tag(ext.TargetHost))
				assert.Equal(t, u.Port(), span.Tag(ext.TargetPort))
				if status == http.StatusOK {
					assert.Nil(t, span.Tag(ext.ErrorMsg))
				} else {
					assert.Equal(t, http.StatusText(status), span.Tag(ext.ErrorMsg))
				}
			})
		}
	}
}

type streamTransport struct {
	fakeTransport
	response *http.Response
	err      error
	request  *http.Request
}

func (s *streamTransport) Stream(req *http.Request) (*http.Response, error) {
	s.request = req
	return s.response, s.err
}

type untouchedBody struct{ reads, closes int }

func (b *untouchedBody) Read([]byte) (int, error) {
	b.reads++
	return 0, io.EOF
}
func (b *untouchedBody) Close() error {
	b.closes++
	return nil
}

func TestStreamDelegation(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			body := &untouchedBody{}
			origin := &streamTransport{response: &http.Response{StatusCode: status, Body: body}}
			client := &opensearch.Client{Transport: origin}
			TraceClient(client)
			req, err := http.NewRequest(http.MethodGet, "http://localhost/_search", nil)
			require.NoError(t, err)
			resp, err := client.Stream(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Same(t, origin.response, resp)
			assert.Same(t, body, resp.Body)
			assert.Zero(t, body.reads)
			assert.Zero(t, body.closes)
		})
	}
	t.Run("transport error", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		origin := &streamTransport{err: context.Canceled}
		client := &opensearch.Client{Transport: origin}
		TraceClient(client)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
		require.NoError(t, err)
		resp, err := client.Stream(req)
		if resp != nil {
			resp.Body.Close()
		}
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, context.Canceled)
		assert.ErrorIs(t, origin.request.Context().Err(), context.Canceled)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Equal(t, context.Canceled.Error(), spans[0].Tag(ext.ErrorMsg))
	})
	t.Run("missing method", func(t *testing.T) {
		client := &opensearch.Client{Transport: fakeTransport{}}
		TraceClient(client)
		req, err := http.NewRequest(http.MethodGet, "/", nil)
		require.NoError(t, err)
		resp, err := client.Stream(req)
		if resp != nil {
			resp.Body.Close()
		}
		assert.ErrorIs(t, err, opensearch.ErrTransportMissingMethodStream)
	})
}

type closableTransport struct {
	fakeTransport
	closed bool
	err    error
}

func (c *closableTransport) Close() error {
	c.closed = true
	return c.err
}

type idleClosingTransport struct {
	closed bool
}

func (*idleClosingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }
func (c *idleClosingTransport) CloseIdleConnections()                         { c.closed = true }

func TestClose(t *testing.T) {
	t.Run("custom closer", func(t *testing.T) {
		closeErr := errors.New("close failed")
		origin := &closableTransport{err: closeErr}
		client := &opensearch.Client{Transport: origin}
		TraceClient(client)
		assert.ErrorIs(t, client.Close(), closeErr)
		assert.True(t, origin.closed)
	})
	t.Run("without closer", func(t *testing.T) {
		client := &opensearch.Client{Transport: fakeTransport{}}
		TraceClient(client)
		assert.NoError(t, client.Close())
	})
	t.Run("new client closes idle connections", func(t *testing.T) {
		origin := &idleClosingTransport{}
		client, err := NewClient(opensearch.Config{Transport: origin})
		require.NoError(t, err)
		require.NoError(t, client.Close())
		assert.True(t, origin.closed)
		require.NoError(t, client.Close())
	})
}

func TestStreamLiveRequestBody(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	origin := &streamTransport{response: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}}
	client := &opensearch.Client{Transport: origin}
	TraceClient(client)
	req, err := http.NewRequest(http.MethodPost, "/_search", reader)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() {
		resp, err := client.Stream(req)
		if resp != nil {
			resp.Body.Close()
		}
		result <- err
	}()
	select {
	case err := <-result:
		require.NoError(t, err)
		assert.Same(t, reader, origin.request.Body)
	case <-time.After(5 * time.Second):
		reader.Close()
		<-result
		t.Fatal("Stream blocked before invoking the underlying streamer")
	}
}

func TestStreamResponseWithError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	body := &untouchedBody{}
	origin := &streamTransport{
		response: &http.Response{StatusCode: http.StatusServiceUnavailable, Body: body},
		err:      context.Canceled,
	}
	client := &opensearch.Client{Transport: origin}
	TraceClient(client)
	req, err := http.NewRequest(http.MethodGet, "/_search", nil)
	require.NoError(t, err)
	resp, err := client.Stream(req)
	require.Same(t, origin.response, resp)
	defer resp.Body.Close()
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, body.reads)
	assert.Zero(t, body.closes)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "503", spans[0].Tag(ext.HTTPCode))
	assert.Equal(t, context.Canceled.Error(), spans[0].Tag(ext.ErrorMsg))
}

type discoveryTransport struct{}

func (discoveryTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, context.Canceled
}

type discoveryLogger struct{ done chan error }

func (l *discoveryLogger) LogRoundTrip(req *http.Request, _ *http.Response, err error, _ time.Time, _ time.Duration) error {
	if req == nil {
		l.done <- err
	}
	return nil
}
func (*discoveryLogger) RequestBodyEnabled() bool  { return false }
func (*discoveryLogger) ResponseBodyEnabled() bool { return false }

func TestNewClientStartupDiscovery(t *testing.T) {
	for _, router := range []bool{false, true} {
		t.Run(strconv.FormatBool(router), func(t *testing.T) {
			t.Setenv("OPENSEARCH_GO_ROUTER", strconv.FormatBool(router))
			for range 100 {
				logger := &discoveryLogger{done: make(chan error, 1)}
				cfg := opensearch.Config{Transport: discoveryTransport{}, DisableRetry: true, Logger: logger}
				if !router {
					enabled := true
					cfg.DiscoverNodesOnStart = &enabled
				}
				client, err := NewClient(cfg)
				require.NoError(t, err)
				require.IsType(t, &transport{}, client.Transport)
				select {
				case err := <-logger.done:
					assert.ErrorIs(t, err, context.Canceled)
				case <-time.After(5 * time.Second):
					client.Close()
					t.Fatal("startup discovery did not finish")
				}
				require.NoError(t, client.Close())
			}
		})
	}
}
