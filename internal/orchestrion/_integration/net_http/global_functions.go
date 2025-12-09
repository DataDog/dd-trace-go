// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package nethttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCaseGlobalFunctions tests the aspect replacing global functions.
// See: https://github.com/DataDog/orchestrion/issues/670 and https://github.com/DataDog/orchestrion/issues/674
// The issue is reproduced in this test since the receiver defined below is called `client`, same as the synthetic
// import added by Orchestrion.
type TestCaseGlobalFunctions struct {
	base
}

func (tc *TestCaseGlobalFunctions) Setup(ctx context.Context, t *testing.T) {
	tc.handler = tc.serveMuxHandler()
	tc.base.Setup(ctx, t)
}

func (tc *TestCaseGlobalFunctions) Run(_ context.Context, t *testing.T) {
	cl := newHttpClient(tc.srv.Addr)

	resp, err := cl.Get("/")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

type httpClient struct {
	serverHost string
}

func newHttpClient(serverHost string) *httpClient {
	return &httpClient{
		serverHost: serverHost,
	}
}

func (client *httpClient) Get(path string) (*http.Response, error) {
	return http.Get("http://" + client.serverHost + path)
}
