// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package waf_test

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/stretchr/testify/require"
)

// TestWAF is a simple validation test of the WAF protecting a net/http server. It only mockups the agent and tests that
// the WAF is properly detecting an XSS attempt and that the corresponding security event is being sent to the agent.
func TestWAF(t *testing.T) {
	// Start the HTTP server acting as the agent
	// Its handler counts the number of AppSec API requests and saves the latest event batch sent (only one expected).
	var (
		nbAppSecAPIRequests int
		batch               []byte
	)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if r.RequestURI == "/appsec/proxy/api/v2/appsecevts" {
			nbAppSecAPIRequests++
			var err error
			batch, err = ioutil.ReadAll(r.Body)
			require.NoError(t, err)
		}
	}))

	// Start the tracer along with the fake agent HTTP server
	os.Setenv("DD_APPSEC_ENABLED", "true")
	tracer.Start(tracer.WithAgentAddr(strings.TrimPrefix(agent.URL, "http://")))

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Send an XSS attack
	req, err := http.NewRequest("POST", srv.URL+"/?attack=<script>alert()</script>", nil)
	if err != nil {
		panic(err)
	}
	res, err := srv.Client().Do(req)
	require.NoError(t, err)

	// Check that the handler was properly called
	b, err := ioutil.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, "Hello World!\n", string(b))

	// Stop the tracer so that the AppSec events gets sent
	tracer.Stop()

	// Check that an XSS attack event was reported.
	require.Equal(t, 1, nbAppSecAPIRequests)
	require.True(t, strings.Contains(string(batch), "xss-blocking"))
}
