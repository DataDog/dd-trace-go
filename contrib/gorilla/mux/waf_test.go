// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package mux_test

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	muxtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gorilla/mux"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	"github.com/stretchr/testify/require"
)

func TestAppSec(t *testing.T) {
	t.Run("path-params", func(t *testing.T) {
		// Start the tracer along with the fake agent HTTP server
		mt := mocktracer.Start()
		defer mt.Stop()

		appsec.Start()
		defer appsec.Stop()

		if !appsec.Enabled() {
			t.Skip("appsec disabled")
		}

		// Start and trace an HTTP server
		router := muxtrace.NewRouter()
		router.Handle("/{pathParam}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte("OK!\n"))
		}))

		srv := httptest.NewServer(router)
		defer srv.Close()

		// Forge HTTP request with path parameter
		req, err := http.NewRequest("POST", srv.URL+"/appscan_fingerprint", nil)
		if err != nil {
			panic(err)
		}

		res, err := srv.Client().Do(req)
		require.NoError(t, err)

		// Check that the handler was properly called
		b, err := ioutil.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "OK!\n", string(b))

		spans := mt.FinishedSpans()
		// The request should have the attack attempt event (appsec rule id crs-913-120).
		event := spans[0].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.True(t, strings.Contains(event.(string), "crs-913-120"))
	})
}
