// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

func TestAppSec(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "true")
	testutils.StartAppSec(t)

	if !instr.AppSecEnabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server with some testing routes
	router := chi.NewRouter().With(Middleware())
	router.HandleFunc("/path0.0/{myPathParam0}/path0.1/{myPathParam1}/path0.2/{myPathParam2}/path0.3/*", func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte("Hello World!\n"))
		require.NoError(t, err)
	})
	router.HandleFunc("/*", func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte("Hello World!\n"))
		require.NoError(t, err)
	})
	router.HandleFunc("/body", func(w http.ResponseWriter, r *http.Request) {
		appsec.MonitorParsedHTTPBody(r.Context(), "$globals")
		_, err := w.Write([]byte("Hello Body!\n"))
		require.NoError(t, err)
	})

	srv := httptest.NewServer(router)
	defer srv.Close()

	// Test an LFI attack via path parameters
	t.Run("request-uri", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		// Send an LFI attack (according to appsec rule id crs-930-110)
		req, err := http.NewRequest("POST", srv.URL+"/../../../secret.txt", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		// Check that the server behaved as intended
		require.Equal(t, http.StatusOK, res.StatusCode)
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(b))
		// The span should contain the security event
		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// The first 301 redirection should contain the attack via the request uri
		event := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "server.request.uri.raw"))
		require.True(t, strings.Contains(event, "crs-930-110"))
	})

	// Test a security scanner attack via path parameters
	t.Run("path-params", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		// Send a security scanner attack (according to appsec rule id crs-913-120)
		req, err := http.NewRequest("POST", srv.URL+"/path0.0/param0/path0.1/param1/path0.2/appscan_fingerprint/path0.3/param3", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		// Check that the handler was properly called
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(b))
		require.Equal(t, http.StatusOK, res.StatusCode)
		// The span should contain the security event
		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		event := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "crs-913-120"))
		require.True(t, strings.Contains(event, "myPathParam2"))
		require.True(t, strings.Contains(event, "server.request.path_params"))
	})

	// Test a PHP injection attack via request parsed body
	t.Run("SDK-body", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srv.URL+"/body", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()

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
