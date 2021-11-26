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

	httptrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	"github.com/stretchr/testify/require"
)

// TestWAF is a simple validation test of the WAF protecting a net/http server. It only mockups the agent and tests that
// the WAF is properly detecting an LFI attempt and that the corresponding security event is being sent to the agent.
func TestWAF(t *testing.T) {
	// Start the tracer along with the fake agent HTTP server
	mt := mocktracer.Start()
	defer mt.Stop()

	appsec.Start()
	defer appsec.Stop()

	if appsec.Status() != "enabled" {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server
	mux := httptrace.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello World!\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

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
	// attack attempt event (appsec rule id crs-930-100).
	event := finished[0].Tag("_dd.appsec.json")
	require.NotNil(t, event)
	require.True(t, strings.Contains(event.(string), "crs-930-100"))

	event = finished[1].Tag("_dd.appsec.json")
	require.NotNil(t, event)
	require.True(t, strings.Contains(event.(string), "crs-930-100"))
}
