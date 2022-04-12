// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package appsec_test

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pAppsec "gopkg.in/DataDog/dd-trace-go.v1/appsec"
	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	"github.com/stretchr/testify/require"
)

// TestWAF is a simple validation test of the WAF protecting a net/http server. It only mockups the agent and tests that
// the WAF is properly detecting an LFI attempt and that the corresponding security event is being sent to the agent.
// Additionally, verifies that rule matching through SDK body instrumentation works as expected
func TestWAF(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	mux.HandleFunc("/body", func(w http.ResponseWriter, r *http.Request) {
		pAppsec.MonitorParsedHTTPBody(r.Context(), "$globals")
		w.Write([]byte("Hello Body!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// This needs to be the first test run in order to retrieve WAF metrics correctly. All the tests here
	//are using the same WAF handle, meaning that ruleset metrics will be sent only once on the first request.
	t.Run("metrics", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srv.URL+"/", nil)
		req.Header.Add("User-Agent", "Arachni/v1")
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)

		// Check that the handler was properly called
		b, err := ioutil.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(b))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		event := finished[0].Tag("_dd.appsec.json")
		require.NotNil(t, event)

		// Verify that metrics types follow the RFC. Values checks are performed in waf/waf_test.go tests
		for _, tag := range []string{"_dd.appsec.waf.duration", "_dd.appsec.waf.duration_ext",
			"_dd.appsec.event_rules.error_count", "_dd.appsec.event_rules.loaded"} {
			_, ok := finished[0].Tag(tag).(float64)
			require.True(t, ok)
		}
		for _, tag := range []string{"_dd.appsec.event_rules.errors", "_dd.appsec.event_rules.version",
			"_dd.appsec.waf.version"} {
			_, ok := finished[0].Tag(tag).(string)
			require.True(t, ok)
		}
	})

	t.Run("lfi", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Send an LFI attack
		req, err := http.NewRequest("POST", srv.URL+"/../../../secret.txt", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)

		// Check that the handler was properly called
		b, err := ioutil.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(b))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 2)

		// Two requests were performed by the client request (due to the 301 redirection) and the two should have the LFI
		// attack attempt event (appsec rule id crs-930-110).
		event := finished[0].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.Contains(t, event, "crs-930-110")

		event = finished[1].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.Contains(t, event, "crs-930-110")
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

		// Check that the handler was properly called
		b, err := ioutil.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello Body!\n", string(b))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		event := finished[0].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.True(t, strings.Contains(event.(string), "crs-933-130"))
	})

	t.Run("obfuscation", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		sensitive := "I-m-sensitive-please-dont-expose-me"
		vulnerable := "1234%20union%20select%20*%20from%20credit_cards"

		// Send malicious request with sensitive information
		req, err := http.NewRequest("POST", srv.URL+"/?password="+sensitive+vulnerable+sensitive, nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)

		// Check that the handler was properly called
		b, err := ioutil.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(b))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)

		// Check that the tags don't hold any sensitive information
		event := finished[0].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.Contains(t, event, "crs-942-100")
		require.NotContains(t, event, sensitive)
		require.NotContains(t, event, vulnerable)
	})
}
