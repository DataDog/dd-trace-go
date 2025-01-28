// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/newtelemetry/internal/transport"
)

func TestNewWriter_ValidConfig(t *testing.T) {
	config := WriterConfig{
		TracerConfig: TracerConfig{
			Service: "test-service",
			Env:     "test-env",
			Version: "1.0.0",
		},
		Endpoints: []*http.Request{
			{Method: http.MethodPost, URL: &url.URL{Scheme: "http", Host: "localhost", Path: "/telemetry"}},
		},
	}

	writer, err := NewWriter(config)
	assert.NoError(t, err)
	assert.NotNil(t, writer)
}

func TestNewWriter_NoEndpoints(t *testing.T) {
	config := WriterConfig{
		TracerConfig: TracerConfig{
			Service: "test-service",
			Env:     "test-env",
			Version: "1.0.0",
		},
		Endpoints: []*http.Request{},
	}

	writer, err := NewWriter(config)
	assert.Error(t, err)
	assert.Nil(t, writer)
}

type testPayload struct {
	RequestTypeValue transport.RequestType `json:"request_type"`
	marshalJSON      func() ([]byte, error)
}

func (p *testPayload) MarshalJSON() ([]byte, error) {
	return p.marshalJSON()
}

func (p *testPayload) RequestType() transport.RequestType {
	return p.RequestTypeValue
}

func TestWriter_Flush_Success(t *testing.T) {
	config := WriterConfig{
		TracerConfig: TracerConfig{
			Service: "test-service",
			Env:     "test-env",
			Version: "1.0.0",
		},
	}

	var (
		marshalJSONCalled bool
		payloadReceived   bool
	)

	payload := testPayload{
		RequestTypeValue: "test",
		marshalJSON: func() ([]byte, error) {
			marshalJSONCalled = true
			return []byte(`{"request_type":"test"}`), nil
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		payloadReceived = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL, nil)
	require.NoError(t, err)

	config.Endpoints = append(config.Endpoints, req)
	writer, _ := NewWriter(config)

	bytesSent, err := writer.Flush(&payload)
	require.NoError(t, err)

	assert.NotZero(t, bytesSent)
	assert.True(t, marshalJSONCalled)
	assert.True(t, payloadReceived)
}

func TestWriter_Flush_Failure(t *testing.T) {
	config := WriterConfig{
		TracerConfig: TracerConfig{
			Service: "test-service",
			Env:     "test-env",
			Version: "1.0.0",
		},
	}

	var (
		marshalJSONCalled bool
		payloadReceived   bool
	)

	payload := testPayload{
		RequestTypeValue: "test",
		marshalJSON: func() ([]byte, error) {
			marshalJSONCalled = true
			return []byte(`{"request_type":"test"}`), nil
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		payloadReceived = true
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL, nil)
	require.NoError(t, err)

	config.Endpoints = append(config.Endpoints, req)
	writer, _ := NewWriter(config)

	bytesSent, err := writer.Flush(&payload)
	require.Error(t, err)
	assert.Zero(t, bytesSent)
	assert.True(t, marshalJSONCalled)
	assert.True(t, payloadReceived)
}

func TestWriter_Flush_MultipleEndpoints(t *testing.T) {
	config := WriterConfig{
		TracerConfig: TracerConfig{
			Service: "test-service",
			Env:     "test-env",
			Version: "1.0.0",
		},
	}

	var (
		marshalJSONCalled int
		payloadReceived1  bool
		payloadReceived2  bool
	)

	payload := testPayload{
		RequestTypeValue: "test",
		marshalJSON: func() ([]byte, error) {
			marshalJSONCalled++
			return []byte(`{"request_type":"test"}`), nil
		},
	}

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		payloadReceived1 = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		assert.True(t, payloadReceived1)
		payloadReceived2 = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server2.Close()

	req1, err := http.NewRequest(http.MethodPost, server1.URL, nil)
	require.NoError(t, err)

	config.Endpoints = append(config.Endpoints, req1)
	req2, err := http.NewRequest(http.MethodPost, server2.URL, nil)
	require.NoError(t, err)

	config.Endpoints = append(config.Endpoints, req2)
	writer, _ := NewWriter(config)

	bytesSent, err := writer.Flush(&payload)
	require.ErrorContains(t, err, `telemetry/writer: unexpected status code: "500 Internal Server Error" (received body: "")`)

	assert.NotZero(t, bytesSent)
	assert.Equal(t, 2, marshalJSONCalled)
	assert.True(t, payloadReceived2)
}
