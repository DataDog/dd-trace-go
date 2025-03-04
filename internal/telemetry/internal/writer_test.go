// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry/internal/transport"
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

	results, err := writer.Flush(&payload)
	require.Error(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, http.StatusBadRequest, results[0].StatusCode)
	assert.ErrorContains(t, err, `400 Bad Request`)
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

	results, err := writer.Flush(&payload)
	assert.NoError(t, err)

	assert.Len(t, results, 2)
	assert.Equal(t, http.StatusInternalServerError, results[0].StatusCode)
	assert.ErrorContains(t, results[0].Error, `500 Internal Server Error`)
	assert.Equal(t, time.Duration(0), results[0].CallDuration)
	assert.Zero(t, results[0].PayloadByteSize)

	assert.Equal(t, http.StatusOK, results[1].StatusCode)
	assert.InDelta(t, time.Duration(1), results[1].CallDuration, float64(time.Second))
	assert.NotZero(t, results[1].PayloadByteSize)
	assert.NoError(t, results[1].Error)

	assert.Equal(t, 2, marshalJSONCalled)
	assert.True(t, payloadReceived2)
}

func TestWriterParallel(t *testing.T) {
	config := WriterConfig{
		TracerConfig: TracerConfig{
			Service: "test-service",
			Env:     "test-env",
			Version: "1.0.0",
		},
	}

	var (
		marshalJSONCalled atomic.Int64
		payloadReceived   atomic.Int64
	)

	payload := testPayload{
		RequestTypeValue: "test",
		marshalJSON: func() ([]byte, error) {
			marshalJSONCalled.Add(1)
			return []byte(`{"request_type":"test"}`), nil
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		payloadReceived.Add(1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL, nil)
	require.NoError(t, err)

	// 2 endpoints that just happens to be the same
	config.Endpoints = append(config.Endpoints, req)
	config.Endpoints = append(config.Endpoints, req)

	writer, _ := NewWriter(config)

	const numRequests = 100
	var wg sync.WaitGroup
	wg.Add(numRequests)
	for i := 0; i < numRequests; i++ {
		go func() {
			defer wg.Done()
			_, err := writer.Flush(&payload)
			require.Error(t, err)
		}()
	}

	wg.Wait()

	assert.EqualValues(t, numRequests*2, marshalJSONCalled.Load())
	assert.EqualValues(t, numRequests*2, payloadReceived.Load())
}
