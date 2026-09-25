// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func startAppSecServer(t *testing.T, opts ...Option) string {
	handler := WrapHandler(func(fctx *fasthttp.RequestCtx) {
		fctx.SetStatusCode(http.StatusOK)
		fmt.Fprintf(fctx, "Hello World!\n")
	}, opts...)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := &fasthttp.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		require.NoError(t, srv.Shutdown())
	})
	return "http://" + ln.Addr().String()
}

func TestAppSec(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "true")
	testutils.StartAppSec(t)

	url := startAppSecServer(t)
	client := &http.Client{Timeout: 10 * time.Second}

	// An LFI attack in the request URI (appsec rule crs-930-110).
	t.Run("request-uri", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest(http.MethodGet, url+"/etc/passwd", nil)
		require.NoError(t, err)
		// Sent raw so the path traversal reaches the server unresolved.
		req.URL.Opaque = "//" + req.URL.Host + "/../../../secret.txt"
		res, err := client.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		event, ok := spans[0].Tag("_dd.appsec.json").(string)
		require.True(t, ok, "expected an appsec event on the request span")
		require.Contains(t, event, "server.request.uri.raw")
		require.Contains(t, event, "crs-930-110")
	})

	// A security scanner attack in a request header (appsec rule crs-913-120).
	t.Run("request-headers", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest(http.MethodGet, url+"/", nil)
		require.NoError(t, err)
		req.Header.Set("User-Agent", "Arachni/v1")
		res, err := client.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		event, ok := spans[0].Tag("_dd.appsec.json").(string)
		require.True(t, ok, "expected an appsec event on the request span")
		require.Contains(t, event, "ua0-600-12x")
	})

	// A clean request must still reach the handler and carry no event.
	t.Run("no-event", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := client.Get(url + "/")
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(body))

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		require.Nil(t, spans[0].Tag("_dd.appsec.json"))
	})
}

func TestAppSecBlocking(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	testutils.StartAppSec(t)

	url := startAppSecServer(t)
	client := &http.Client{Timeout: 10 * time.Second}

	// The blocking ruleset blocks on the client IP, which is known before the
	// handler runs, so the handler must never be reached.
	t.Run("block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest(http.MethodGet, url+"/", nil)
		require.NoError(t, err)
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		res, err := client.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, "application/json", res.Header.Get("Content-Type"))
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NotContains(t, string(body), "Hello World!")

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		require.Equal(t, "403", spans[0].Tag("http.status_code"))
		require.NotNil(t, spans[0].Tag("_dd.appsec.json"))
	})

	t.Run("block-html", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest(http.MethodGet, url+"/", nil)
		require.NoError(t, err)
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		req.Header.Set("Accept", "text/html")
		res, err := client.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, "text/html", res.Header.Get("Content-Type"))
	})

	t.Run("no-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := client.Get(url + "/")
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(body))
	})
}
