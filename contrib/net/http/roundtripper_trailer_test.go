// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

type trailerReader struct {
	io.Reader
	trailer http.Header
}

func (r trailerReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.trailer.Set("Lambda-Runtime-Function-Error-Body", "late failure")
	}
	return n, err
}

func TestRoundTripperStreamingTrailer(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, "response bytes", string(body))
		assert.Equal(t, "late failure", r.Trailer.Get("Lambda-Runtime-Function-Error-Body"))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	trailer := http.Header{"Lambda-Runtime-Function-Error-Body": nil}
	req, err := http.NewRequest(http.MethodPost, server.URL, trailerReader{strings.NewReader("response bytes"), trailer})
	require.NoError(t, err)
	req.Trailer = trailer
	client := WrapClient(&http.Client{})
	resp, err := client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Empty(t, req.Header, "trace propagation must still use a copy of the headers")
}
