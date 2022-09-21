// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package opentracing

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pappsec "gopkg.in/DataDog/dd-trace-go.v1/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	"github.com/opentracing-contrib/go-stdlib/nethttp"
	"github.com/opentracing/opentracing-go/mocktracer"
	"github.com/stretchr/testify/require"
)

func TestAppSec(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server with some testing routes
	mux := http.NewServeMux()
	mux.HandleFunc("/body", func(w http.ResponseWriter, r *http.Request) {
		pappsec.MonitorParsedHTTPBody(r.Context(), "$globals")
		_, err := w.Write([]byte("Hello Body!\n"))
		require.NoError(t, err)
	})

	// Test an LFI attack via path parameters
	t.Run("request-uri", func(t *testing.T) {
		mt := mocktracer.New()
		srv := httptest.NewServer(nethttp.Middleware(mt, AppSecMiddleware(mux)))
		defer srv.Close()

		// Send an LFI attack (according to appsec rule id crs-930-110)
		req, err := http.NewRequest("POST", srv.URL+"/../../../secret.txt", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		// Check that the server behaved as intended (404 after the 301)
		require.Equal(t, http.StatusNotFound, res.StatusCode)
		// The span should contain the security event
		finished := mt.FinishedSpans()
		require.Len(t, finished, 2) // 301 + 404

		// The first 301 redirection should contain the attack via the request uri
		event := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "server.request.uri.raw"))
		require.True(t, strings.Contains(event, "crs-930-110"))
		// The second request should contain the event via the referrer header
		event = finished[1].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "server.request.headers.no_cookies"))
		require.True(t, strings.Contains(event, "crs-930-110"))
	})

	t.Run("response-status", func(t *testing.T) {
		mt := mocktracer.New()
		srv := httptest.NewServer(nethttp.Middleware(mt, AppSecMiddleware(mux)))
		defer srv.Close()

		req, err := http.NewRequest("POST", srv.URL+"/etc/", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		require.Equal(t, 404, res.StatusCode)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		event := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "server.response.status"))
		require.True(t, strings.Contains(event, "nfd-000-001"))
	})

	// Test a PHP injection attack via request parsed body
	t.Run("SDK-body", func(t *testing.T) {
		mt := mocktracer.New()
		srv := httptest.NewServer(nethttp.Middleware(mt, AppSecMiddleware(mux)))
		defer srv.Close()

		req, err := http.NewRequest("POST", srv.URL+"/body", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		// Check that the handler was properly called
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello Body!\n", string(b))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		event := finished[0].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.True(t, strings.Contains(event.(string), "crs-933-130"))
	})
}
