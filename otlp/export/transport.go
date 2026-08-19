// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const (
	pathTraces  = "/v1/traces"
	pathMetrics = "/v1/metrics"
	pathLogs    = "/v1/logs"

	headerContentType = "Content-Type"
	contentTypeProto  = "application/x-protobuf"
	headerAPIKey      = "dd-api-key"

	headerMetricConfig        = "dd-otel-metric-config"
	metricConfigDistributions = `{"histograms":{"mode":"distributions"}}`

	initialBackoff = 100 * time.Millisecond
	maxBackoff     = time.Second
	maxRetryAfter  = 60 * time.Second
	responseLimit  = 4 << 20
)

var (
	// ErrRequestTooLarge identifies a request rejected before an HTTP attempt
	// because its encoded size exceeded the configured limit.
	ErrRequestTooLarge  = errors.New("otlp/export: request exceeds maximum encoded size")
	errResponseTooLarge = errors.New("otlp/export: response exceeds 4 MiB")
)

type rawTransport struct {
	client                *http.Client
	endpoint              *url.URL
	headers               http.Header
	datadog               bool
	apiKey                string
	maxAttempts           uint
	maxRequestSize        int
	requestTimeout        time.Duration
	defaultRequestTimeout time.Duration
}

func newRawTransport(cfg *clientConfig) *rawTransport {
	headers := make(http.Header, len(cfg.headers))
	for name, value := range cfg.headers {
		headers.Set(name, value)
	}
	return &rawTransport{
		client:                cfg.httpClient,
		endpoint:              cfg.endpoint,
		headers:               headers,
		datadog:               cfg.route == routeDatadog,
		apiKey:                cfg.apiKey,
		maxAttempts:           cfg.maxAttempts,
		maxRequestSize:        cfg.maxRequestSize,
		requestTimeout:        cfg.requestTimeout,
		defaultRequestTimeout: cfg.defaultRequestTimeout,
	}
}

func (t *rawTransport) submit(ctx context.Context, path string, message proto.Message) (RequestResult, []byte) {
	body, err := proto.Marshal(message)
	if err != nil {
		return RequestResult{Err: fmt.Errorf("otlp/export: marshal: %w", err)}, nil
	}
	if t.maxRequestSize > 0 && len(body) > t.maxRequestSize {
		return RequestResult{Err: fmt.Errorf("%w: %d bytes, maximum %d", ErrRequestTooLarge, len(body), t.maxRequestSize)}, nil
	}

	result, err := t.submitBody(ctx, path, body)
	requestResult := RequestResult{
		StatusCode: result.statusCode,
		Attempts:   result.attempts,
		Retriable:  result.retriable,
		Err:        err,
	}
	if err != nil {
		requestResult.ResponseSnippet = responseSnippet(result.body)
	}
	if statusMessage := otlpStatusMessage(result.body); err != nil && statusMessage != "" {
		requestResult.ResponseSnippet = responseSnippet([]byte(statusMessage))
	}
	return requestResult, result.body
}

type submitResult struct {
	statusCode int
	attempts   int
	body       []byte
	retriable  bool
}

type attemptResult struct {
	statusCode int
	body       []byte
	retryAfter time.Duration
	terminal   bool
	err        error
}

func (t *rawTransport) submitBody(ctx context.Context, path string, body []byte) (submitResult, error) {
	result := submitResult{}
	backoff := initialBackoff
	for attempt := uint(1); ; attempt++ {
		result.attempts = int(attempt)
		current := t.doPost(ctx, path, body)
		result.statusCode = current.statusCode
		result.body = current.body
		if current.err == nil {
			result.retriable = false
			return result, nil
		}

		result.retriable = !current.terminal && otlpRetriable(ctx, current.statusCode)
		if !result.retriable || attempt >= t.maxAttempts {
			return result, current.err
		}

		wait := jitter(backoff)
		if current.retryAfter > 0 {
			wait = current.retryAfter
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			result.retriable = false
			return result, ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (t *rawTransport) doPost(ctx context.Context, path string, body []byte) attemptResult {
	timeout := t.requestTimeout
	if timeout == 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			timeout = t.defaultRequestTimeout
		}
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, t.signalURL(path), bytes.NewReader(body))
	if err != nil {
		return attemptResult{err: err}
	}
	request.Header = t.requestHeaders(path)

	response, err := t.client.Do(request)
	if err != nil {
		return attemptResult{err: err}
	}
	defer response.Body.Close()

	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	responseTooLarge := len(responseBody) > responseLimit
	if responseTooLarge {
		responseBody = responseBody[:responseLimit]
		readErr = errors.Join(readErr, errResponseTooLarge)
	}
	_, drainErr := io.Copy(io.Discard, response.Body)
	readErr = errors.Join(readErr, drainErr)
	if responseTooLarge {
		return attemptResult{
			statusCode: response.StatusCode,
			body:       responseBody,
			terminal:   true,
			err:        fmt.Errorf("otlp/export: read response body: %w", readErr),
		}
	}
	if response.StatusCode == http.StatusOK {
		if readErr != nil {
			return attemptResult{body: responseBody, err: fmt.Errorf("otlp/export: read response body: %w", readErr)}
		}
		return attemptResult{statusCode: response.StatusCode, body: responseBody}
	}
	return attemptResult{
		statusCode: response.StatusCode,
		body:       responseBody,
		retryAfter: parseRetryAfter(response.Header),
		err:        fmt.Errorf("otlp/export: unexpected status %d", response.StatusCode),
	}
}

func (t *rawTransport) signalURL(path string) string {
	return t.endpoint.JoinPath(path).String()
}

func (t *rawTransport) requestHeaders(path string) http.Header {
	headers := t.headers.Clone()
	headers.Set(headerContentType, contentTypeProto)
	if t.datadog {
		headers.Set(headerAPIKey, t.apiKey)
		if path == pathMetrics {
			headers.Set(headerMetricConfig, metricConfigDistributions)
		}
	}
	return headers
}

func jitter(duration time.Duration) time.Duration {
	if duration <= 0 {
		return duration
	}
	return duration/2 + time.Duration(rand.Int64N(int64(duration/2)+1))
}

func otlpRetriable(ctx context.Context, status int) bool {
	if ctx.Err() != nil {
		return false
	}
	switch status {
	case 0, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func parseRetryAfter(headers http.Header) time.Duration {
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		if seconds > int64(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		return clampRetryAfter(time.Until(retryAt))
	}
	return 0
}

func clampRetryAfter(delay time.Duration) time.Duration {
	switch {
	case delay <= 0:
		return 0
	case delay > maxRetryAfter:
		return maxRetryAfter
	default:
		return delay
	}
}

func otlpStatusMessage(body []byte) string {
	for len(body) > 0 {
		field, fieldType, tagSize := protowire.ConsumeTag(body)
		if tagSize < 0 {
			return ""
		}
		body = body[tagSize:]
		if field == 2 && fieldType == protowire.BytesType {
			value, valueSize := protowire.ConsumeBytes(body)
			if valueSize < 0 {
				return ""
			}
			return string(value)
		}
		fieldSize := protowire.ConsumeFieldValue(field, fieldType, body)
		if fieldSize < 0 {
			return ""
		}
		body = body[fieldSize:]
	}
	return ""
}

// Go can replay a redirected POST as a GET and forward credentials to the new host.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
