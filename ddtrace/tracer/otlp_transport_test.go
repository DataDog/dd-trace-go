// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOTLPTransportSendSuccess(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := newOTLPTransport(srv.Client(), srv.URL, nil)
	err := tr.send([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), received)
}

func TestOTLPTransportSendHeaders(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := newOTLPTransport(srv.Client(), srv.URL, map[string]string{
		"Api-Key":  "secret",
		"X-Custom": "value",
	})
	err := tr.send([]byte("data"))
	require.NoError(t, err)

	assert.Equal(t, "application/x-protobuf", gotHeaders.Get("Content-Type"), "default Content-Type must be set")
	assert.Equal(t, "secret", gotHeaders.Get("Api-Key"))
	assert.Equal(t, "value", gotHeaders.Get("X-Custom"))
}

func TestOTLPTransportSendHTTPMethod(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr := newOTLPTransport(srv.Client(), srv.URL, nil)
	err := tr.send([]byte("data"))
	require.NoError(t, err)
	assert.Equal(t, "POST", gotMethod)
}

func TestOTLPTransportSendErrorStatus(t *testing.T) {
	tests := []struct {
		code int
		text string
	}{
		{http.StatusBadRequest, "Bad Request"},
		{http.StatusUnauthorized, "Unauthorized"},
		{http.StatusInternalServerError, "Internal Server Error"},
		{http.StatusServiceUnavailable, "Service Unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.code)
			}))
			defer srv.Close()

			tr := newOTLPTransport(srv.Client(), srv.URL, nil)
			err := tr.send([]byte("data"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.text)
		})
	}
}

func TestOTLPTransportSendConnectionError(t *testing.T) {
	tr := newOTLPTransport(http.DefaultClient, "http://127.0.0.1:0/nonexistent", nil)
	err := tr.send([]byte("data"))
	require.Error(t, err)
}

func TestOTLPTransportConnectionReuse(t *testing.T) {
	var connCount int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("response body that must be drained"))
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			atomic.AddInt64(&connCount, 1)
		}
	}
	srv.Start()
	defer srv.Close()

	tr := newOTLPTransport(srv.Client(), srv.URL, nil)
	for range 5 {
		require.NoError(t, tr.send([]byte("data")))
	}
	assert.Equal(t, int64(1), atomic.LoadInt64(&connCount),
		"expected a single connection to be reused across sends")
}
