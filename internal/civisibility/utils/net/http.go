// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package net

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"time"

	"github.com/tinylib/msgp/msgp"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// Constants for common strings
const (
	ContentTypeJSON            = "application/json"
	ContentTypeJSONAlternative = "application/vnd.api+json"
	ContentTypeOctetStream     = "application/octet-stream"
	ContentTypeMessagePack     = "application/msgpack"
	ContentEncodingGzip        = "gzip"
	HeaderContentType          = "Content-Type"
	HeaderContentEncoding      = "Content-Encoding"
	HeaderAcceptEncoding       = "Accept-Encoding"
	HeaderRateLimitReset       = "x-ratelimit-reset"
	HTTPStatusTooManyRequests  = 429
	FormatJSON                 = "json"
	FormatMessagePack          = "msgpack"
)

// FormFile represents a file to be uploaded in a multipart form request.
type FormFile struct {
	FieldName   string      // The name of the form field
	FileName    string      // The name of the file
	Content     interface{} // The content of the file (can be []byte, map, struct, etc.)
	ContentType string      // The MIME type of the file (e.g., "application/json", "application/octet-stream")
}

// RequestConfig holds configuration for a request.
type RequestConfig struct {
	Method     string            // HTTP method: GET or POST
	URL        string            // Request URL
	Headers    map[string]string // Additional HTTP headers
	Body       interface{}       // Request body for JSON, MessagePack, or raw bytes
	Format     string            // Format: "json" or "msgpack"
	Compressed bool              // Whether to use gzip compression
	Files      []FormFile        // Files to be uploaded in a multipart form data request
	MaxRetries int               // Maximum number of retries
	Backoff    time.Duration     // Initial backoff duration for retries
}

// Response represents the HTTP response with deserialization capabilities and status code.
type Response struct {
	Body         []byte // Response body in raw format
	Format       string // Format of the response (json or msgpack)
	StatusCode   int    // HTTP status code
	CanUnmarshal bool   // Whether the response body can be unmarshalled
	Compressed   bool   // Whether to use gzip compression
}

// Unmarshal deserializes the response body into the provided target based on the response format.
func (r *Response) Unmarshal(target interface{}) error {
	if !r.CanUnmarshal {
		return fmt.Errorf("cannot unmarshal response with status code %d", r.StatusCode)
	}

	switch r.Format {
	case FormatJSON:
		return json.Unmarshal(r.Body, target)
	case FormatMessagePack:
		if target.(msgp.Unmarshaler) != nil {
			_, err := target.(msgp.Unmarshaler).UnmarshalMsg(r.Body)
			return err
		} else {
			return errors.New("target must implement msgp.Unmarshaler for MessagePack unmarshalling")
		}
	default:
		return fmt.Errorf("unsupported format '%s' for unmarshalling", r.Format)
	}
}

// RequestHandler handles HTTP requests with retries and different formats.
type RequestHandler struct {
	Client *http.Client
}

// NewRequestHandler creates a new RequestHandler with a default HTTP client.
func NewRequestHandler() *RequestHandler {
	return &RequestHandler{
		Client: &http.Client{
			Timeout: 45 * time.Second, // Customize timeout as needed
		},
	}
}

// NewRequestHandlerWithClient creates a new RequestHandler with a custom http.Client
func NewRequestHandlerWithClient(client *http.Client) *RequestHandler {
	return &RequestHandler{
		Client: client,
	}
}

// SendRequest sends an HTTP request based on the provided configuration.
func (rh *RequestHandler) SendRequest(config RequestConfig) (*Response, error) {
	if config.MaxRetries <= 0 {
		config.MaxRetries = DefaultMaxRetries // Default retries
	}
	if config.Backoff <= 0 {
		config.Backoff = DefaultBackoff // Default backoff
	}
	if config.Method == "" {
		return nil, errors.New("HTTP method is required")
	}
	if config.URL == "" {
		return nil, errors.New("URL is required")
	}

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		stopRetries, rs, err := rh.internalSendRequest(&config, attempt)
		if stopRetries {
			return rs, err
		}
	}

	return nil, errors.New("max retries exceeded")
}

func (rh *RequestHandler) internalSendRequest(config *RequestConfig, attempt int) (stopRetries bool, response *Response, requestError error) {
	var req *http.Request

	// Check if it's a multipart form data request
	if len(config.Files) > 0 {
		// Create multipart form data body
		body, contentType, err := createMultipartFormData(config.Files, config.Compressed)
		if err != nil {
			return true, nil, err
		}

		if log.DebugEnabled() {
			var files []string
			for _, f := range config.Files {
				files = append(files, f.FieldName)
			}
			log.Debug("ciVisibilityHttpClient: new request with files [method: %v, url: %v, attempt: %v, maxRetries: %v] %v",
				config.Method, config.URL, attempt, config.MaxRetries, files)
		}
		req, err = http.NewRequest(config.Method, config.URL, bytes.NewBuffer(body))
		if err != nil {
			return true, nil, err
		}
		req.Header.Set(HeaderContentType, contentType)
		if config.Compressed {
			req.Header.Set(HeaderContentEncoding, ContentEncodingGzip)
		}
	} else if config.Body != nil {
		// Handle JSON body
		serializedBody, err := serializeData(config.Body, config.Format)
		if err != nil {
			return true, nil, err
		}

		if log.DebugEnabled() {
			log.Debug("ciVisibilityHttpClient: new request with body [method: %v, url: %v, attempt: %v, maxRetries: %v, compressed: %v] %v bytes",
				config.Method, config.URL, attempt, config.MaxRetries, config.Compressed, len(serializedBody))
		}

		// Compress body if needed
		if config.Compressed {
			serializedBody, err = compressData(serializedBody)
			if err != nil {
				return true, nil, err
			}
		}

		req, err = http.NewRequest(config.Method, config.URL, bytes.NewBuffer(serializedBody))
		if err != nil {
			return true, nil, err
		}
		if config.Format == FormatJSON {
			req.Header.Set(HeaderContentType, ContentTypeJSON)
		}
		if config.Format == FormatMessagePack {
			req.Header.Set(HeaderContentType, ContentTypeMessagePack)
		}
		if config.Compressed {
			req.Header.Set(HeaderContentEncoding, ContentEncodingGzip)
		}
	} else {
		// Handle requests without a body (e.g., GET requests)
		var err error
		req, err = http.NewRequest(config.Method, config.URL, nil)
		if err != nil {
			return true, nil, err
		}

		log.Debug("ciVisibilityHttpClient: new request [method: %v, url: %v, attempt: %v, maxRetries: %v]",
			config.Method, config.URL, attempt, config.MaxRetries)
	}

	// Set that is possible to handle gzip responses
	req.Header.Set(HeaderAcceptEncoding, ContentEncodingGzip)

	// Add custom headers if provided
	for key, value := range config.Headers {
		req.Header.Set(key, value)
	}

	resp, err := rh.Client.Do(req)
	if err != nil {
		log.Debug("ciVisibilityHttpClient: error = %v", err)
		// Retry if there's an error
		exponentialBackoff(attempt, config.Backoff)
		return false, nil, nil
	}
	// Close response body
	defer resp.Body.Close()

	// Capture the status code
	statusCode := resp.StatusCode

	// Check for rate-limiting (HTTP 429)
	if resp.StatusCode == HTTPStatusTooManyRequests {
		log.Debug("ciVisibilityHttpClient: response status code = %v", resp.StatusCode)

		rateLimitReset := resp.Header.Get(HeaderRateLimitReset)
		if rateLimitReset != "" {
			if resetTime, err := strconv.ParseInt(rateLimitReset, 10, 64); err == nil {
				var waitDuration time.Duration
				if resetTime > time.Now().Unix() {
					// Assume it's a Unix timestamp
					waitDuration = time.Until(time.Unix(resetTime, 0))
				} else {
					// Assume it's a duration in seconds
					waitDuration = time.Duration(resetTime) * time.Second
				}
				if waitDuration > 0 {
					time.Sleep(waitDuration)
				}
				return false, nil, nil
			}
		}

		// Fallback to exponential backoff if header is missing or invalid
		exponentialBackoff(attempt, config.Backoff)
		return false, nil, nil
	}

	// Check status code for retries
	if statusCode >= 406 {
		// Retry if the status code is >= 406
		log.Debug("ciVisibilityHttpClient: response status code = %v", resp.StatusCode)
		exponentialBackoff(attempt, config.Backoff)
		return false, nil, nil
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return true, nil, err
	}

	// Decompress response if it is gzip compressed
	compressedResponse := false
	if resp.Header.Get(HeaderContentEncoding) == ContentEncodingGzip {
		compressedResponse = true
		responseBody, err = decompressData(responseBody)
		if err != nil {
			return true, nil, err
		}
	}

	// Determine response format from headers
	responseFormat := "unknown"
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get(HeaderContentType))
	if err == nil {
		if mediaType == ContentTypeJSON || mediaType == ContentTypeJSONAlternative {
			responseFormat = FormatJSON
		}
	}

	if log.DebugEnabled() {
		log.Debug("ciVisibilityHttpClient: response received [method: %v, url: %v, status_code: %v, format: %v] %v bytes",
			config.Method, config.URL, resp.StatusCode, responseFormat, len(responseBody))
	}

	// Determine if we can unmarshal based on status code (2xx)
	canUnmarshal := statusCode >= 200 && statusCode < 300

	// Return the successful response with status code and unmarshal capability
	return true, &Response{Body: responseBody, Format: responseFormat, StatusCode: statusCode, CanUnmarshal: canUnmarshal, Compressed: compressedResponse}, nil
}

// Helper functions for data serialization, compression, and handling multipart form data

// serializeData serializes the data based on the format.
func serializeData(data interface{}, format string) ([]byte, error) {
	switch v := data.(type) {
	case []byte:
		// If it's already a byte array, use it directly
		return v, nil
	case io.Reader:
		// If it's an io.Reader, read the content
		return io.ReadAll(v)
	default:
		// Otherwise, serialize it according to the specified format
		if format == FormatJSON {
			return json.Marshal(data)
		}
		if format == FormatMessagePack {
			if data.(msgp.Marshaler) != nil {
				return data.(msgp.Marshaler).MarshalMsg([]byte{})
			}
			return nil, errors.New("data must implement msgp.Marshaler for MessagePack serialization")
		}
	}
	return nil, fmt.Errorf("unsupported format '%s' for data type '%T'", format, data)
}

// compressData compresses the data using gzip.
func compressData(data []byte) ([]byte, error) {
	if data == nil {
		return nil, errors.New("attempt to compress a nil data array")
	}

	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	_, err := writer.Write(data)
	if err != nil {
		return nil, err
	}
	writer.Close()
	return buf.Bytes(), nil
}

// decompressData decompresses gzip data.
func decompressData(data []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %v", err)
	}
	defer reader.Close()
	decompressedData, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress data: %v", err)
	}
	return decompressedData, nil
}

// exponentialBackoff performs an exponential backoff with retries.
func exponentialBackoff(retryCount int, initialDelay time.Duration) {
	maxDelay := 30 * time.Second
	delay := initialDelay * (1 << uint(retryCount)) // Exponential backoff
	if delay > maxDelay {
		delay = maxDelay
	}
	time.Sleep(delay)
}

// prepareContent prepares the content for a FormFile by serializing it if needed.
func prepareContent(content interface{}, contentType string) ([]byte, error) {
	if contentType == ContentTypeJSON {
		return serializeData(content, FormatJSON)
	} else if contentType == ContentTypeMessagePack {
		return serializeData(content, FormatMessagePack)
	} else if contentType == ContentTypeOctetStream {
		// For binary data, ensure it's already in byte format
		if data, ok := content.([]byte); ok {
			return data, nil
		}
		if reader, ok := content.(io.Reader); ok {
			return io.ReadAll(reader)
		}
		return nil, errors.New("content must be []byte or an io.Reader for octet-stream content type")
	}
	return nil, errors.New("unsupported content type for serialization")
}

// createMultipartFormData creates a multipart form data request body with the given files.
// It also compresses the data using gzip if compression is enabled.
func createMultipartFormData(files []FormFile, compressed bool) ([]byte, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	for _, file := range files {
		partHeaders := textproto.MIMEHeader{}
		if file.FileName == "" {
			partHeaders.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"`, file.FieldName))
		} else {
			partHeaders.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, file.FieldName, file.FileName))
		}
		partHeaders.Set("Content-Type", file.ContentType)

		part, err := writer.CreatePart(partHeaders)
		if err != nil {
			return nil, "", err
		}

		// Prepare the file content (serialize if necessary based on content type)
		fileContent, err := prepareContent(file.Content, file.ContentType)
		if err != nil {
			return nil, "", err
		}

		if _, err := part.Write(fileContent); err != nil {
			return nil, "", err
		}
	}

	// Close the writer to set the terminating boundary
	err := writer.Close()
	if err != nil {
		return nil, "", err
	}

	// Compress the multipart form data if compression is enabled
	if compressed {
		compressedData, err := compressData(buf.Bytes())
		if err != nil {
			return nil, "", err
		}
		return compressedData, writer.FormDataContentType(), nil
	}

	return buf.Bytes(), writer.FormDataContentType(), nil
}
