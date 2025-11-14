// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
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

func TestNewWriter_ProcessTags(t *testing.T) {
	cfg := WriterConfig{
		TracerConfig: TracerConfig{
			Service: "test-service",
			Env:     "test-env",
			Version: "1.0.0",
		},
		Endpoints: []*http.Request{
			{Method: http.MethodPost, URL: &url.URL{Scheme: "http", Host: "localhost", Path: "/telemetry"}},
		},
	}

	t.Run("enabled", func(t *testing.T) {
		w, err := NewWriter(cfg)
		require.NoError(t, err)

		body := w.(*writer).body
		assert.NotEmpty(t, body.Application.ProcessTags)
	})
	t.Run("disabled", func(t *testing.T) {
		t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", "false")
		processtags.Reload()

		w, err := NewWriter(cfg)
		require.NoError(t, err)

		body := w.(*writer).body
		assert.Empty(t, body.Application.ProcessTags)
	})
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
		marshalJSONCalled atomic.Bool
		payloadReceived   atomic.Bool
	)

	payload := testPayload{
		RequestTypeValue: "test",
		marshalJSON: func() ([]byte, error) {
			marshalJSONCalled.Store(true)
			return []byte(`{"request_type":"test"}`), nil
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		r.Body.Close()
		payloadReceived.Store(true)
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
	assert.True(t, marshalJSONCalled.Load())
	assert.True(t, payloadReceived.Load())
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
		marshalJSONCalled atomic.Bool
		payloadReceived   atomic.Bool
	)

	payload := testPayload{
		RequestTypeValue: "test",
		marshalJSON: func() ([]byte, error) {
			marshalJSONCalled.Store(true)
			return []byte(`{"request_type":"test"}`), nil
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		r.Body.Close()
		payloadReceived.Store(true)
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
	assert.ErrorContains(t, err, `400 Bad Request`)
	assert.Equal(t, http.StatusBadRequest, results[0].StatusCode)
	assert.True(t, marshalJSONCalled.Load())
	assert.True(t, payloadReceived.Load())
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
		marshalJSONCalled atomic.Int64
		payloadReceived1  atomic.Bool
		payloadReceived2  atomic.Bool
	)

	payload := testPayload{
		RequestTypeValue: "test",
		marshalJSON: func() ([]byte, error) {
			marshalJSONCalled.Add(1)
			return []byte(`{"request_type":"test"}`), nil
		},
	}

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		r.Body.Close()
		payloadReceived1.Store(true)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		r.Body.Close()
		assert.True(t, payloadReceived1.Load())
		payloadReceived2.Store(true)
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
	assert.ErrorContains(t, results[0].Error, `500 Internal Server Error`)
	assert.Equal(t, http.StatusInternalServerError, results[0].StatusCode)
	assert.Equal(t, time.Duration(0), results[0].CallDuration)
	assert.Zero(t, results[0].PayloadByteSize)

	assert.Equal(t, http.StatusOK, results[1].StatusCode)
	assert.InDelta(t, time.Duration(1), results[1].CallDuration, float64(time.Second))
	assert.NotZero(t, results[1].PayloadByteSize)
	assert.NoError(t, results[1].Error)

	assert.EqualValues(t, 2, marshalJSONCalled.Load())
	assert.True(t, payloadReceived2.Load())
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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		r.Body.Close()
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

	time.Sleep(50 * time.Millisecond) // ensure all marshalJSON calls are done

	assert.EqualValues(t, numRequests*2, marshalJSONCalled.Load())
	assert.EqualValues(t, numRequests*2, payloadReceived.Load())
}
