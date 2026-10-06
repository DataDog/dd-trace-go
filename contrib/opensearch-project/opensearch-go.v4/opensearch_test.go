// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package opensearch

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
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
		err = client.Transport.(opensearchtransport.Discoverable).DiscoverNodes()
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
