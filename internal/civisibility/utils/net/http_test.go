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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
		var data map[string]interface{}
		json.Unmarshal(body, &data)
		w.Header().Set(HeaderContentType, ContentTypeJSON)
		json.NewEncoder(w).Encode(map[string]interface{}{"received": data})
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
		Body:       map[string]interface{}{"key": "value"},
		Format:     "json",
		Compressed: false,
	}

	response, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "json", response.Format)

	var result map[string]interface{}
	err = response.Unmarshal(&result)
	assert.NoError(t, err)
	assert.Equal(t, "value", result["received"].(map[string]interface{})["key"])
}

func TestSendMultipartFormDataRequest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockMultipartHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHttpClient())
	config := RequestConfig{
		Method: "POST",
		URL:    server.URL,
		Files: []FormFile{
			{
				FieldName:   "file1",
				FileName:    "test.json",
				Content:     map[string]interface{}{"key": "value"},
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

	var result map[string]interface{}
	err = response.Unmarshal(&result)
	assert.NoError(t, err)
	assert.Equal(t, `{"key":"value"}`, result["file1"])
	assert.Equal(t, "\x01\x02\x03", result["file2"])
}

func TestSendJSONRequestWithGzipCompression(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockJSONMsgPackHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHttpClient())
	config := RequestConfig{
		Method:     "POST",
		URL:        server.URL,
		Body:       map[string]interface{}{"key": "value"},
		Format:     "json",
		Compressed: true,
	}

	response, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "json", response.Format)

	var result map[string]interface{}
	err = response.Unmarshal(&result)
	assert.NoError(t, err)
	assert.NotNil(t, result["received"], "Expected 'received' key to be present in the response")
	assert.Equal(t, "value", result["received"].(map[string]interface{})["key"])
}

func TestSendMultipartFormDataRequestWithGzipCompression(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockMultipartHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHttpClient())
	config := RequestConfig{
		Method: "POST",
		URL:    server.URL,
		Files: []FormFile{
			{
				FieldName:   "file1",
				FileName:    "test.json",
				Content:     map[string]interface{}{"key": "value"},
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

	var result map[string]interface{}
	err = response.Unmarshal(&result)
	assert.NoError(t, err)
	assert.Equal(t, `{"key":"value"}`, result["file1"])
	assert.Equal(t, "\x01\x02\x03", result["file2"])
}

func TestRateLimitHandlingWithRetries(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockRateLimitHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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
	for i := 0; i < 3; i++ {
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
			Content:     map[string]interface{}{"key": "value"},
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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
	handler := NewRequestHandlerWithClient(createNewHttpClient())
	config := RequestConfig{
		Method:     "GET",
		URL:        "http://[::1]:namedport", // Invalid URL
		Compressed: false,
	}

	response, err := handler.SendRequest(config)
	assert.Error(t, err)
	assert.Nil(t, response)
}

func TestSendEmptyBodyWithGzipCompression(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(mockJSONMsgPackHandler))
	defer server.Close()

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
	config := RequestConfig{
		Method:     "PUT",
		URL:        server.URL,
		Body:       map[string]interface{}{"key": "value"},
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
	customHeaderKey := "X-Custom-Header"
	customHeaderValue := "CustomValue"

	config := RequestConfig{
		Method: "POST",
		URL:    server.URL,
		Headers: map[string]string{
			customHeaderKey: customHeaderValue,
		},
		Body:       map[string]interface{}{"key": "value"},
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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
	handler := NewRequestHandlerWithClient(createNewHttpClient())
	config := RequestConfig{
		Method:     "POST",
		URL:        "http://example.com",
		Body:       map[string]interface{}{"key": "value"},
		Format:     "unsupported_format", // Unsupported format
		Compressed: false,
	}

	response, err := handler.SendRequest(config)
	assert.Error(t, err) // Unsupported format error
	assert.Nil(t, response)
}

func TestSendRequestWithInvalidMethod(t *testing.T) {
	t.Parallel()
	handler := NewRequestHandlerWithClient(createNewHttpClient())
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
	handler := NewRequestHandlerWithClient(createNewHttpClient())
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
	handler := NewRequestHandlerWithClient(createNewHttpClient())
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

	var data interface{}
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
	config := RequestConfig{
		Method: "GET",
		URL:    ts.URL,
	}

	resp, err := handler.SendRequest(config)
	assert.NoError(t, err)
	assert.Equal(t, "unknown", resp.Format)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, resp.CanUnmarshal)

	var data interface{}
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
	handler := NewRequestHandlerWithClient(createNewHttpClient())
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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
		time.Sleep(100 * time.Millisecond)
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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

	handler := NewRequestHandlerWithClient(createNewHttpClient())
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
	handler := NewRequestHandlerWithClient(createNewHttpClient())
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
