// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package gatewayapi

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/stretchr/testify/require"
)

func TestRequestMirror(t *testing.T) {
	testutils.StartAppSec(t)
	if !instr.AppSecEnabled() {
		t.Skip("appsec disabled")
	}

	srv := httptest.NewServer(HTTPRequestMirrorHandler(Config{}))
	defer srv.Close()

	t.Run("query", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Send a request to the server
		_, err := srv.Client().Get(srv.URL + "/?x=$globals")
		require.Error(t, err, io.EOF, "EOF is expected because the connection is hijacked and closed")

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		// Check common span tags
		require.Equal(t, "server", spans[0].Tag("span.kind"))
		require.Equal(t, "GET /", spans[0].Tag("resource.name"))
		require.Equal(t, "GET", spans[0].Tag("http.method"))

		// Check appsec event
		require.NotEmpty(t, spans[0].Tag("appsec.event"))
		require.NotEmpty(t, spans[0].Tag("_dd.appsec.enabled"))

		// Check no response tags
		require.Empty(t, spans[0].Tag("http.status_code"))
	})

	t.Run("body", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Send a request to the server
		_, err := srv.Client().Post(srv.URL, "application/json", strings.NewReader(`{"x":"$globals"}`))
		require.Error(t, err, io.EOF, "EOF is expected because the connection is hijacked and closed")

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)

		// Check common span tags
		require.Equal(t, "server", spans[0].Tag("span.kind"))
		require.Equal(t, "POST /", spans[0].Tag("resource.name"))
		require.Equal(t, "POST", spans[0].Tag("http.method"))

		// Check appsec event
		require.NotEmpty(t, spans[0].Tag("appsec.event"))
		require.NotEmpty(t, spans[0].Tag("_dd.appsec.enabled"))

		// Check no response tags
		require.Empty(t, spans[0].Tag("http.status_code"))
	})
}
