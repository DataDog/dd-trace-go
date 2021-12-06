// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build appsec
// +build appsec

package echo_test

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	echotrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/labstack/echo.v4"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestAppSec(t *testing.T) {
	// Start the tracer along with the fake agent HTTP server
	mt := mocktracer.Start()
	defer mt.Stop()

	appsec.Start()
	defer appsec.Stop()

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server
	e := echo.New()
	e.Use(echotrace.Middleware())

	e.POST("/*tmp", func(c echo.Context) error {
		return c.String(200, "Hello World!\n")
	})
	srv := httptest.NewServer(e)
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
	require.Len(t, finished, 1)

	// The request should have the LFI attack attempt event (appsec rule id crs-930-100).
	event := finished[0].Tag("_dd.appsec.json")
	require.NotNil(t, event)
	require.True(t, strings.Contains(event.(string), "crs-930-100"))
}
