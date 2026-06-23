// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	stdnet "net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const DefaultMultipartMemorySize = 10 << 20 // 10MB

// Mock server handlers for different test scenarios

// Mock server for basic JSON and MessagePack requests with gzip handling
func mockJSONMsgPackHandler(w http.ResponseWriter, r *http.Request) {
	var body []byte
	var err error

	// Check if the request is gzip compressed and decompress it
	if r.Header.Get(HeaderContentEncoding) == ContentEncodingGzip {
		gzipReader, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "failed to decompress gzip", http.StatusBadRequest)
			return
		}
		defer gzipReader.Close()
		body, err = io.ReadAll(gzipReader)
	} else {
		body, err = io.ReadAll(r.Body)
	}

	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// Process JSON based on Content-Type
	if r.Header.Get(HeaderContentType) == ContentTypeJSON {
		var data map[string]any
		json.Unmarshal(body, &data)
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		json.NewEncoder(w).Encode(map[string]any{"received": data})
	}
}

// Mock server for multipart form data with gzip handling
func mockMultipartHandler(w http.ResponseWriter, r *http.Request) {
	var err error

	// Check if the request is gzip compressed and decompress it
	if r.Header.Get(HeaderContentEncoding) == ContentEncodingGzip {
		gzipReader, err := gzip.NewReader(r.Body)
		if err != nil {
			http.Error(w, "failed to decompress gzip", http.StatusBadRequest)
			return
		}
		defer gzipReader.Close()

		// Replace the request body with the decompressed body for further processing
		r.Body = io.NopCloser(gzipReader)
	}

	// Parse multipart form data
	err = r.ParseMultipartForm(DefaultMultipartMemorySize)
	if err != nil {
		http.Error(w, "cannot parse multipart form", http.StatusBadRequest)
		return
	}

	response := make(map[string]string)
	for key := range r.MultipartForm.File {
		file, _, _ := r.FormFile(key)
		content, _ := io.ReadAll(file)
		response[key] = string(content)
	}

	w.Header().Set(HeaderContentType, ContentTypeJSON)
	json.NewEncoder(w).Encode(response)
}

// Mock server for rate limiting with predictable reset timing
func mockRateLimitHandler(w http.ResponseWriter, _ *http.Request) {
	// Set the rate limit reset time to 2 seconds
	w.Header().Set(HeaderRateLimitReset, "2")
	http.Error(w, "Too Many Requests", HTTPStatusTooManyRequests)
}

// Test Suite

func TestSendJSONRequest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockJSONMsgPackHandler))
	defer server.Close()

	handler := NewRequestHandler()
	config := RequestConfig{
		Method:     "POST",
		URL:        server.URL,
		Body:       map[string]any{"key": "value"},
		Format:     "json",
		Compressed: false,
	}

	response, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "json", response.Format)

	var result map[string]any
	err = response.Unmarshal(&result)
	assert.NoError(t, err)
	assert.Equal(t, "value", result["received"].(map[string]any)["key"])
}

func TestCloseIdleConnectionsClosesDefaultHTTPClientIdleConnections(t *testing.T) {
	originalDefaultHTTPClient := defaultHTTPClient
	defaultHTTPClient = createNewHTTPClient()
	t.Cleanup(func() {
		CloseIdleConnections()
		defaultHTTPClient = originalDefaultHTTPClient
	})

	idleConn := make(chan struct{}, 1)
	closedConn := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		_, _ = w.Write([]byte(`{}`))
	}))
	server.Config.ConnState = func(_ stdnet.Conn, state http.ConnState) {
		switch state {
		case http.StateIdle:
			signalConnectionState(idleConn)
		case http.StateClosed:
			signalConnectionState(closedConn)
		}
	}
	server.Start()
	t.Cleanup(server.Close)

	response, err := NewRequestHandler().SendRequest(RequestConfig{
		Method: "GET",
		URL:    server.URL,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)

	waitForConnectionState(t, idleConn, "idle")
	CloseIdleConnections()
	waitForConnectionState(t, closedConn, "closed")
}

func TestRequestHandlerCloseIdleConnectionsClosesCustomHTTPClientIdleConnections(t *testing.T) {
	idleConn := make(chan struct{}, 1)
	closedConn := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		_, _ = w.Write([]byte(`{}`))
	}))
	server.Config.ConnState = func(_ stdnet.Conn, state http.ConnState) {
		switch state {
		case http.StateIdle:
			signalConnectionState(idleConn)
		case http.StateClosed:
			signalConnectionState(closedConn)
		}
	}
	server.Start()
	t.Cleanup(server.Close)

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	response, err := handler.SendRequest(RequestConfig{
		Method: "GET",
		URL:    server.URL,
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)

	waitForConnectionState(t, idleConn, "idle")
	handler.CloseIdleConnections()
	waitForConnectionState(t, closedConn, "closed")
}

func TestSendMultipartFormDataRequest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockMultipartHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method: "POST",
		URL:    server.URL,
		Files: []FormFile{
			{
				FieldName:   "file1",
				FileName:    "test.json",
				Content:     map[string]any{"key": "value"},
				ContentType: ContentTypeJSON,
			},
			{
				FieldName:   "file2",
				FileName:    "test.bin",
				Content:     []byte{0x01, 0x02, 0x03},
				ContentType: ContentTypeOctetStream,
			},
		},
	}

	response, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "json", response.Format)

	var result map[string]any
	err = response.Unmarshal(&result)
	assert.NoError(t, err)
	assert.Equal(t, `{"key":"value"}`, result["file1"])
	assert.Equal(t, "\x01\x02\x03", result["file2"])
}

func TestRequestConfigDoesNotExposeRawBodyFields(t *testing.T) {
	t.Parallel()

	requestConfigType := reflect.TypeFor[RequestConfig]()

	_, hasRawBody := requestConfigType.FieldByName("RawBody")
	assert.False(t, hasRawBody)

	_, hasRawContentType := requestConfigType.FieldByName("RawContentType")
	assert.False(t, hasRawContentType)
}

func TestSendRequestPrebuiltMultipartBodyUsesCallerContentTypeAndExactBytes(t *testing.T) {
	t.Parallel()

	files := []FormFile{
		{
			FieldName:   "event",
			FileName:    "event.json",
			Content:     map[string]string{"type": "coverage_report", "format": FormatLCOV},
			ContentType: ContentTypeJSON,
		},
		{
			FieldName:   "coverage",
			FileName:    "coverage.gz",
			Content:     []byte("compressed-coverage"),
			ContentType: ContentTypeOctetStream,
		},
	}
	body, contentType, err := createMultipartFormData(files, false)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, contentType, r.Header.Get(HeaderContentType))
		require.Empty(t, r.Header.Get(HeaderContentEncoding))
		require.Equal(t, int64(len(body)), r.ContentLength)

		receivedBody, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, body, receivedBody)

		parts := readMultipartBodyParts(t, contentType, receivedBody)
		require.JSONEq(t, `{"format":"lcov","type":"coverage_report"}`, string(parts["event"]))
		require.Equal(t, []byte("compressed-coverage"), parts["coverage"])

		w.Header().Set(HeaderContentType, ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	response, err := NewRequestHandlerWithClient(createNewHTTPClient()).SendRequest(RequestConfig{
		Method: "POST",
		URL:    server.URL,
		Headers: map[string]string{
			HeaderContentType: contentType,
		},
		Body: body,
	})

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
}

func TestSendRequestPrebuiltByteBodyHeadersOverrideFormatContentType(t *testing.T) {
	t.Parallel()

	files := []FormFile{
		{
			FieldName:   "payload",
			FileName:    "payload.bin",
			Content:     []byte("multipart-payload"),
			ContentType: ContentTypeOctetStream,
		},
	}
	body, contentType, err := createMultipartFormData(files, false)
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, contentType, r.Header.Get(HeaderContentType))
		receivedBody, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, body, receivedBody)

		parts := readMultipartBodyParts(t, contentType, receivedBody)
		require.Equal(t, []byte("multipart-payload"), parts["payload"])

		w.Header().Set(HeaderContentType, ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	response, err := NewRequestHandlerWithClient(createNewHTTPClient()).SendRequest(RequestConfig{
		Method: "POST",
		URL:    server.URL,
		Headers: map[string]string{
			HeaderContentType: contentType,
		},
		Body:   body,
		Format: FormatJSON,
	})

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
}

func TestSendRequestPrebuiltMultipartBodyRetainsExactBytesOnRetry(t *testing.T) {
	t.Parallel()

	files := []FormFile{
		{
			FieldName:   "payload",
			FileName:    "payload.bin",
			Content:     []byte("retry-payload"),
			ContentType: ContentTypeOctetStream,
		},
	}
	body, contentType, err := createMultipartFormData(files, false)
	require.NoError(t, err)

	var receivedBodies [][]byte
	var receivedContentTypes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		receivedBodies = append(receivedBodies, append([]byte(nil), receivedBody...))
		receivedContentTypes = append(receivedContentTypes, r.Header.Get(HeaderContentType))

		if len(receivedBodies) == 1 {
			w.Header().Set(HeaderContentEncoding, ContentEncodingGzip)
			w.Header().Set(HeaderContentType, ContentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not valid gzip data"))
			return
		}

		parts := readMultipartBodyParts(t, contentType, receivedBody)
		require.Equal(t, []byte("retry-payload"), parts["payload"])
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	response, err := NewRequestHandlerWithClient(createNewHTTPClient()).SendRequest(RequestConfig{
		Method: "POST",
		URL:    server.URL,
		Headers: map[string]string{
			HeaderContentType: contentType,
		},
		Body:       body,
		MaxRetries: 2,
		Backoff:    10 * time.Millisecond,
	})

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Len(t, receivedBodies, 2)
	require.Equal(t, body, receivedBodies[0])
	require.Equal(t, body, receivedBodies[1])
	require.Equal(t, []string{contentType, contentType}, receivedContentTypes)
}

func TestSendRequestPrebuiltMultipartReaderBodyRetainsExactBytesOnRetry(t *testing.T) {
	t.Parallel()

	files := []FormFile{
		{
			FieldName:   "payload",
			FileName:    "payload.bin",
			Content:     []byte("reader-retry-payload"),
			ContentType: ContentTypeOctetStream,
		},
	}
	body, contentType, err := createMultipartFormData(files, false)
	require.NoError(t, err)

	var receivedBodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		receivedBodies = append(receivedBodies, append([]byte(nil), receivedBody...))

		if len(receivedBodies) == 1 {
			w.Header().Set(HeaderContentEncoding, ContentEncodingGzip)
			w.Header().Set(HeaderContentType, ContentTypeJSON)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not valid gzip data"))
			return
		}

		require.Equal(t, contentType, r.Header.Get(HeaderContentType))
		parts := readMultipartBodyParts(t, contentType, receivedBody)
		require.Equal(t, []byte("reader-retry-payload"), parts["payload"])
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success": true}`))
	}))
	defer server.Close()

	response, err := NewRequestHandlerWithClient(createNewHTTPClient()).SendRequest(RequestConfig{
		Method: "POST",
		URL:    server.URL,
		Headers: map[string]string{
			HeaderContentType: contentType,
		},
		Body:       bytes.NewReader(body),
		MaxRetries: 2,
		Backoff:    10 * time.Millisecond,
	})

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Len(t, receivedBodies, 2)
	require.Equal(t, body, receivedBodies[0])
	require.Equal(t, body, receivedBodies[1])
}

func TestSendJSONRequestWithGzipCompression(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockJSONMsgPackHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "POST",
		URL:        server.URL,
		Body:       map[string]any{"key": "value"},
		Format:     "json",
		Compressed: true,
	}

	response, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "json", response.Format)

	var result map[string]any
	err = response.Unmarshal(&result)
	assert.NoError(t, err)
	assert.NotNil(t, result["received"], "Expected 'received' key to be present in the response")
	assert.Equal(t, "value", result["received"].(map[string]any)["key"])
}

func TestSendMultipartFormDataRequestWithGzipCompression(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockMultipartHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method: "POST",
		URL:    server.URL,
		Files: []FormFile{
			{
				FieldName:   "file1",
				FileName:    "test.json",
				Content:     map[string]any{"key": "value"},
				ContentType: ContentTypeJSON,
			},
			{
				FieldName:   "file2",
				FileName:    "test.bin",
				Content:     []byte{0x01, 0x02, 0x03},
				ContentType: ContentTypeOctetStream,
			},
		},
		Compressed: true,
	}

	response, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "json", response.Format)

	var result map[string]any
	err = response.Unmarshal(&result)
	assert.NoError(t, err)
	assert.Equal(t, `{"key":"value"}`, result["file1"])
	assert.Equal(t, "\x01\x02\x03", result["file2"])
}

func TestRateLimitHandlingWithRetries(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockRateLimitHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "GET", // No body needed for GET
		URL:        server.URL,
		Compressed: true, // Enable gzip compression for GET
		MaxRetries: 1,
		Backoff:    1 * time.Second, // Exponential backoff fallback
	}

	start := time.Now()
	response, err := handler.SendRequest(config)
	elapsed := time.Since(start)

	// Since the rate limit is set to reset after 2 seconds, and we retry twice,
	// the minimum elapsed time should be at least 4 seconds (2s for each retry).
	assert.Error(t, err)
	assert.Nil(t, response)
	assert.True(t, elapsed >= 4*time.Second, "Expected at least 4 seconds due to rate limit retry delay")
}

func TestGzipDecompressionError(t *testing.T) {
	t.Parallel()
	// Simulate corrupted gzip data
	corruptedData := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff, 0x03, 0x00}

	_, err := decompressData(corruptedData)
	assert.Error(t, err)
}

func TestExponentialBackoffDelays(t *testing.T) {
	t.Parallel()

	// Simulate exponential backoff with 3 retries and 1-second initial delay
	var duration time.Duration
	for i := range 3 {
		duration = duration + getExponentialBackoffDuration(i, 1*time.Second)
	}

	assert.True(t, duration >= 7*time.Second, "Expected at least 7 seconds due to exponential backoff")
}

func TestCreateMultipartFormDataWithUnsupportedContentType(t *testing.T) {
	t.Parallel()
	files := []FormFile{
		{
			FieldName:   "file1",
			FileName:    "test.unknown",
			Content:     map[string]any{"key": "value"},
			ContentType: "unsupported/content-type", // Unsupported content type
		},
	}

	_, _, err := createMultipartFormData(files, false)
	assert.Error(t, err)
}

func TestRateLimitHandlingWithoutResetHeader(t *testing.T) {
	t.Parallel()
	// Mock server without 'x-ratelimit-reset' header
	mockRateLimitHandlerWithoutHeader := func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Too Many Requests", HTTPStatusTooManyRequests)
	}

	server := httptest.NewServer(http.HandlerFunc(mockRateLimitHandlerWithoutHeader))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "GET", // No body needed for GET
		URL:        server.URL,
		Compressed: false,
		MaxRetries: 1,
		Backoff:    1 * time.Second,
	}

	start := time.Now()
	response, err := handler.SendRequest(config)
	elapsed := time.Since(start)

	// With exponential backoff fallback, the minimum elapsed time should be at least 3 seconds (1s + 2s)
	assert.Error(t, err)
	assert.Nil(t, response)
	assert.True(t, elapsed >= 3*time.Second, "Expected at least 3 seconds due to exponential backoff delay")
}

func TestSendRequestWithInvalidURL(t *testing.T) {
	t.Parallel()
	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "GET",
		URL:        "http://[::1]:namedport", // Invalid URL
		Compressed: false,
	}

	response, err := handler.SendRequest(config)
	assert.Error(t, err)
	assert.Nil(t, response)
}

// signalConnectionState records a connection state transition without blocking
// the HTTP server connection-state callback.
func signalConnectionState(state chan<- struct{}) {
	select {
	case state <- struct{}{}:
	default:
	}
}

// waitForConnectionState waits for an expected server connection-state
// transition produced by the HTTP transport under test.
func waitForConnectionState(t *testing.T, state <-chan struct{}, stateName string) {
	t.Helper()
	select {
	case <-state:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for connection to become %s", stateName)
	}
}

func TestSendEmptyBodyWithGzipCompression(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockJSONMsgPackHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "POST",
		URL:        server.URL,
		Body:       nil, // Empty body
		Format:     "json",
		Compressed: true,
	}

	response, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
}

func TestCompressDataWithInvalidInput(t *testing.T) {
	t.Parallel()
	// Attempt to compress an invalid data type (e.g., an empty interface{})
	_, err := compressData(nil)
	assert.Error(t, err)
}

func TestSendPUTRequestWithJSONBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockJSONMsgPackHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "PUT",
		URL:        server.URL,
		Body:       map[string]any{"key": "value"},
		Format:     "json",
		Compressed: false,
	}

	response, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
}

func TestSendDELETERequest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockJSONMsgPackHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "DELETE",
		URL:        server.URL,
		Compressed: false,
	}

	response, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
}

func TestSendHEADRequest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockJSONMsgPackHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "HEAD",
		URL:        server.URL,
		Compressed: false,
	}

	response, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
}

func TestSendRequestWithCustomHeaders(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockJSONMsgPackHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	customHeaderKey := "X-Custom-Header"
	customHeaderValue := "CustomValue"

	config := RequestConfig{
		Method: "POST",
		URL:    server.URL,
		Headers: map[string]string{
			customHeaderKey: customHeaderValue,
		},
		Body:       map[string]any{"key": "value"},
		Format:     "json",
		Compressed: false,
	}

	response, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)

	// Verify that the custom header was correctly set
	assert.Equal(t, customHeaderValue, config.Headers[customHeaderKey])
}

func TestSendRequestWithTimeout(t *testing.T) {
	t.Parallel()
	// Mock server that delays response
	mockSlowHandler := func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second) // Delay longer than the client timeout
		w.WriteHeader(http.StatusOK)
	}

	server := httptest.NewServer(http.HandlerFunc(mockSlowHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	handler.Client.Timeout = 1 * time.Second // Set client timeout to 2 seconds

	config := RequestConfig{
		Method:     "GET",
		URL:        server.URL,
		MaxRetries: 1,
	}

	response, err := handler.SendRequest(config)
	assert.Error(t, err)
	assert.Nil(t, response)
}

func TestSendRequestWithMaxRetriesExceeded(t *testing.T) {
	t.Parallel()
	// Mock server that always returns a 500 error
	mockAlwaysFailHandler := func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}

	server := httptest.NewServer(http.HandlerFunc(mockAlwaysFailHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "GET",
		URL:        server.URL,
		Compressed: false,
		MaxRetries: 1, // Only retry twice
		Backoff:    500 * time.Millisecond,
	}

	start := time.Now()
	response, err := handler.SendRequest(config)
	elapsed := time.Since(start)

	// Ensure retries were attempted
	assert.Error(t, err)
	assert.Nil(t, response)
	assert.True(t, elapsed >= 1*time.Second, "Expected at least 1 second due to retry delay")
}

func TestGzipResponseDecompressionHandling(t *testing.T) {
	t.Parallel()
	// Mock server that returns a gzip-compressed response
	mockGzipResponseHandler := func(w http.ResponseWriter, _ *http.Request) {
		originalResponse := `{"message": "Hello, Gzip!"}`
		var buf bytes.Buffer
		gzipWriter := gzip.NewWriter(&buf)
		_, err := gzipWriter.Write([]byte(originalResponse))
		if err != nil {
			http.Error(w, "Failed to compress response", http.StatusInternalServerError)
			return
		}
		gzipWriter.Close()

		// Set headers and write compressed data
		w.Header().Set(HeaderContentEncoding, ContentEncodingGzip)
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		w.Write(buf.Bytes())
	}

	server := httptest.NewServer(http.HandlerFunc(mockGzipResponseHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "GET",
		URL:        server.URL,
		Compressed: false, // Compression not needed for request, only testing response decompression
	}

	response, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "json", response.Format)

	// Check that the response body was correctly decompressed
	var result map[string]string
	err = response.Unmarshal(&result)
	assert.NoError(t, err)
	assert.Equal(t, "Hello, Gzip!", result["message"])
}

func TestSendRequestWithUnsupportedFormat(t *testing.T) {
	t.Parallel()
	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "POST",
		URL:        "http://example.com",
		Body:       map[string]any{"key": "value"},
		Format:     "unsupported_format", // Unsupported format
		Compressed: false,
	}

	response, err := handler.SendRequest(config)
	assert.Error(t, err) // Unsupported format error
	assert.Nil(t, response)
}

func TestSendRequestWithInvalidMethod(t *testing.T) {
	t.Parallel()
	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method: "",
		URL:    "http://example.com",
	}

	_, err := handler.SendRequest(config)
	assert.Error(t, err)
	assert.EqualError(t, err, "HTTP method is required")
}

func TestSendRequestWithEmptyURL(t *testing.T) {
	t.Parallel()
	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method: "GET",
		URL:    "",
	}

	_, err := handler.SendRequest(config)
	assert.Error(t, err)
	assert.EqualError(t, err, "URL is required")
}

func TestSendRequestWithNetworkError(t *testing.T) {
	t.Parallel()
	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:  "GET",
		URL:     "http://invalid-url",
		Backoff: 10 * time.Millisecond,
	}

	_, err := handler.SendRequest(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max retries exceeded")
}

func TestSerializeNilDataToJSON(t *testing.T) {
	t.Parallel()
	data, err := serializeData(nil, FormatJSON)
	assert.NoError(t, err)
	assert.Equal(t, []byte("null"), data)
}

func TestCompressEmptyData(t *testing.T) {
	t.Parallel()
	data, err := compressData([]byte{})
	assert.NoError(t, err)
	assert.NotEmpty(t, data)
}

func TestDecompressValidGzipData(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	writer.Write([]byte("test data"))
	writer.Close()

	data, err := decompressData(buf.Bytes())
	assert.NoError(t, err)
	assert.Equal(t, []byte("test data"), data)
}

func TestExponentialBackoffWithNegativeRetryCount(t *testing.T) {
	t.Parallel()
	start := time.Now()
	exponentialBackoff(-1, 100*time.Millisecond)
	duration := time.Since(start)
	assert.LessOrEqual(t, duration, 100*time.Millisecond)
}

func TestResponseUnmarshalWithUnsupportedFormat(t *testing.T) {
	t.Parallel()
	resp := &Response{
		Body:         []byte("data"),
		Format:       "unknown",
		StatusCode:   http.StatusOK,
		CanUnmarshal: true,
	}

	var data any
	err := resp.Unmarshal(&data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format 'unknown'")
}

func TestSendRequestWithUnsupportedResponseFormat(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, "application/xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<data>test</data>"))
	}))
	defer ts.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method: "GET",
		URL:    ts.URL,
	}

	resp, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, "unknown", resp.Format)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, resp.CanUnmarshal)

	var data any
	err = resp.Unmarshal(&data)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format 'unknown'")
}

func TestPrepareContentWithNonByteContentForOctetStream(t *testing.T) {
	t.Parallel()
	_, err := prepareContent(12345, ContentTypeOctetStream)
	assert.Error(t, err)
	assert.EqualError(t, err, "content must be []byte or an io.Reader for octet-stream content type")
}

func TestCreateMultipartFormDataWithCompression(t *testing.T) {
	t.Parallel()
	files := []FormFile{
		{
			FieldName:   "file1",
			FileName:    "test.txt",
			Content:     []byte("test content"),
			ContentType: ContentTypeOctetStream,
		},
	}

	data, contentType, err := createMultipartFormData(files, true)
	assert.NoError(t, err)
	assert.Contains(t, contentType, "multipart/form-data; boundary=")
	assert.NotEmpty(t, data)

	// Decompress the data to verify the content
	decompressedData, err := decompressData(data)
	assert.NoError(t, err)
	assert.Contains(t, string(decompressedData), "test content")
}

func TestSendRequestWithBodySerializationError(t *testing.T) {
	t.Parallel()
	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method: "POST",
		URL:    "http://example.com",
		Body:   make(chan int), // Channels cannot be serialized
		Format: FormatJSON,
	}

	_, err := handler.SendRequest(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported type: chan int")
}

func TestSendRequestWithCompressedResponse(t *testing.T) {
	t.Parallel()
	// Server that returns a compressed response
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		w.Header().Set(HeaderContentEncoding, ContentEncodingGzip)
		var buf bytes.Buffer
		writer := gzip.NewWriter(&buf)
		writer.Write([]byte(`{"message": "compressed response"}`))
		writer.Close()
		w.WriteHeader(http.StatusOK)
		w.Write(buf.Bytes())
	}))
	defer ts.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "GET",
		URL:        ts.URL,
		Compressed: true,
	}

	resp, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, FormatJSON, resp.Format)
	assert.True(t, resp.CanUnmarshal)

	var data map[string]string
	err = resp.Unmarshal(&data)
	assert.NoError(t, err)
	assert.Equal(t, "compressed response", data["message"])
}

func TestSendRequestWithRetryAfterHeader(t *testing.T) {
	t.Parallel()
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts == 0 {
			w.Header().Set(HeaderRateLimitReset, "1") // Wait 1 second
			w.WriteHeader(HTTPStatusTooManyRequests)
			attempts++
			return
		}
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer ts.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "GET",
		URL:        ts.URL,
		MaxRetries: 2,
		Backoff:    100 * time.Millisecond,
	}

	start := time.Now()
	resp, err := handler.SendRequest(config)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, resp.CanUnmarshal)
	assert.GreaterOrEqual(t, duration, time.Second) // Ensures wait time was respected

	var data map[string]bool
	err = resp.Unmarshal(&data)
	assert.NoError(t, err)
	assert.True(t, data["success"])
}

func TestSendRequestWithInvalidRetryAfterHeader(t *testing.T) {
	t.Parallel()
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts == 0 {
			w.Header().Set(HeaderRateLimitReset, "invalid") // Invalid value
			w.WriteHeader(HTTPStatusTooManyRequests)
			attempts++
			return
		}
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer ts.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "GET",
		URL:        ts.URL,
		MaxRetries: 2,
		Backoff:    100 * time.Millisecond,
	}

	start := time.Now()
	resp, err := handler.SendRequest(config)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, resp.CanUnmarshal)
	assert.GreaterOrEqual(t, duration, 100*time.Millisecond) // Backoff was used

	var data map[string]bool
	err = resp.Unmarshal(&data)
	assert.NoError(t, err)
	assert.True(t, data["success"])
}

func TestExponentialBackoffWithMaxDelay(t *testing.T) {
	t.Parallel()
	delay := getExponentialBackoffDuration(10, 1*time.Second) // Should be limited to maxDelay (10s)
	assert.LessOrEqual(t, delay, 11*time.Second)
}

func TestSendRequestWithContextTimeout(t *testing.T) {
	t.Parallel()
	handler := &RequestHandler{
		Client: &http.Client{
			Timeout: 50 * time.Millisecond,
		},
	}

	// Server that sleeps longer than client timeout
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	config := RequestConfig{
		Method: "GET",
		URL:    ts.URL,
	}

	_, err := handler.SendRequest(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max retries exceeded")
}

func TestSendRequestWithRateLimitButNoResetHeader(t *testing.T) {
	t.Parallel()
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts < 2 {
			w.WriteHeader(HTTPStatusTooManyRequests)
			attempts++
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer ts.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "GET",
		URL:        ts.URL,
		MaxRetries: 3,
		Backoff:    100 * time.Millisecond,
	}

	start := time.Now()
	resp, err := handler.SendRequest(config)
	duration := time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.GreaterOrEqual(t, duration, 300*time.Millisecond)
	assert.Equal(t, []byte("OK"), resp.Body)
}

func TestSendRequestWhenServerClosesConnection(t *testing.T) {
	t.Parallel()
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h1 := w.(http.Hijacker)
		conn, _, _ := h1.Hijack()
		conn.Close()
	}))
	ts.EnableHTTP2 = false // Disable HTTP/2 to allow hijacking
	ts.Start()
	defer ts.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "GET",
		URL:        ts.URL,
		MaxRetries: 1,
		Backoff:    100 * time.Millisecond,
	}

	_, err := handler.SendRequest(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max retries exceeded")
}

func TestSendRequestWithInvalidPortAndMaxRetriesExceeded(t *testing.T) {
	t.Parallel()
	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "GET",
		URL:        "http://localhost:0", // Invalid port to force error
		MaxRetries: 2,
		Backoff:    10 * time.Millisecond,
	}

	_, err := handler.SendRequest(config)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max retries exceeded")
}

func TestPrepareContentWithNilContent(t *testing.T) {
	t.Parallel()
	data, err := prepareContent(nil, ContentTypeJSON)
	assert.NoError(t, err)
	assert.Equal(t, []byte("null"), data)
}

func TestSerializeDataWithInvalidDataType(t *testing.T) {
	t.Parallel()
	_, err := serializeData(make(chan int), FormatJSON)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported type: chan int")
}

func TestSendRequestWithBodyDecompressErrorRetries(t *testing.T) {
	t.Parallel()
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if attempts == 0 {
			// First attempt: claim gzip but send garbage → decompress error → must retry
			w.Header().Set(HeaderContentEncoding, ContentEncodingGzip)
			w.Header().Set(HeaderContentType, ContentTypeJSON)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("not valid gzip data"))
			attempts++
			return
		}
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer ts.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "GET",
		URL:        ts.URL,
		MaxRetries: 2,
		Backoff:    10 * time.Millisecond,
	}

	resp, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, resp.CanUnmarshal)

	var data map[string]bool
	err = resp.Unmarshal(&data)
	assert.NoError(t, err)
	assert.True(t, data["success"])
}

// TestSendRequestBodyReaderRetainsContentOnRetry verifies that when a request
// body is an io.Reader and a retry is triggered by a bad gzip response, the
// retry sends the full original payload rather than an empty/drained reader.
func TestSendRequestBodyReaderRetainsContentOnRetry(t *testing.T) {
	t.Parallel()
	const payload = `{"logs":"data"}`
	attempts := 0
	var receivedBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts == 0 {
			// Trigger decompress-error retry path: claim gzip, send garbage.
			w.Header().Set(HeaderContentEncoding, ContentEncodingGzip)
			w.Header().Set(HeaderContentType, ContentTypeJSON)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("not valid gzip data"))
			attempts++
			return
		}
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer ts.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method:     "POST",
		URL:        ts.URL,
		Body:       bytes.NewReader([]byte(payload)),
		Format:     FormatJSON,
		MaxRetries: 2,
		Backoff:    10 * time.Millisecond,
	}

	resp, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, payload, receivedBody, "retry must send the full original body, not a drained reader")
}

// TestSendRequestFilesReaderRetainsContentOnRetry verifies that when a
// multipart file's Content is an io.Reader and a retry is triggered, the retry
// sends the full original file content rather than empty bytes.
func TestSendRequestFilesReaderRetainsContentOnRetry(t *testing.T) {
	t.Parallel()
	const filePayload = "binary-file-content"
	attempts := 0
	var receivedFileContent string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts == 0 {
			// Trigger decompress-error retry path.
			w.Header().Set(HeaderContentEncoding, ContentEncodingGzip)
			w.Header().Set(HeaderContentType, ContentTypeJSON)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("not valid gzip data"))
			attempts++
			return
		}
		if err := r.ParseMultipartForm(DefaultMultipartMemorySize); err != nil {
			http.Error(w, "bad multipart", http.StatusBadRequest)
			return
		}
		if f, _, err := r.FormFile("payload"); err == nil {
			content, _ := io.ReadAll(f)
			receivedFileContent = string(content)
		}
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success": true}`))
	}))
	defer ts.Close()

	handler := NewRequestHandlerWithClient(createNewHTTPClient())
	config := RequestConfig{
		Method: "POST",
		URL:    ts.URL,
		Files: []FormFile{
			{
				FieldName:   "payload",
				FileName:    "data.bin",
				Content:     bytes.NewReader([]byte(filePayload)),
				ContentType: ContentTypeOctetStream,
			},
		},
		MaxRetries: 2,
		Backoff:    10 * time.Millisecond,
	}

	resp, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, filePayload, receivedFileContent, "retry must send the full original file content, not a drained reader")
}

func readMultipartBodyParts(t *testing.T, contentType string, body []byte) map[string][]byte {
	t.Helper()

	mediaType, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	require.NotEmpty(t, params["boundary"])

	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	parts := map[string][]byte{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(part.Header.Get(HeaderContentType), "application/"))
		partBody, err := io.ReadAll(part)
		require.NoError(t, err)
		parts[part.FormName()] = partBody
		require.NoError(t, part.Close())
	}
	return parts
}
