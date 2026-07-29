// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/errortrace"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	headerEVPSubdomain   = "X-Datadog-EVP-Subdomain"
	headerRateLimitReset = "x-ratelimit-reset"
)

const (
	// EndpointEvalMetric and EndpointLLMSpan (with their EVP subdomains
	// SubdomainEvalMetric and SubdomainLLMSpan) are the LLM Obs evaluation-metric
	// and span intake paths. They are exported so the offline export clients (see
	// llmobs/export) can reuse this package's routing instead of re-declaring the
	// paths; the live tracer paths above use them directly.
	EndpointEvalMetric = "/api/intake/llm-obs/v2/eval-metric"
	EndpointLLMSpan    = "/api/v2/llmobs"

	endpointPrefixEVPProxy  = "/evp_proxy/v2"
	endpointPrefixDNE       = "/api/unstable/llm-obs/v1"
	endpointPrefixDNEStable = "/api/v2/llm-obs/v1"

	SubdomainLLMSpan    = "llmobs-intake"
	SubdomainEvalMetric = "api"
	subdomainDNE        = "api"
)

const (
	defaultSite            = "datadoghq.com"
	defaultMaxRetries uint = 3

	defaultTimeout           = 5 * time.Second
	bulkUploadTimeout        = 60 * time.Second
	getDatasetRecordsTimeout = 20 * time.Second
)

var (
	ErrDatasetNotFound = errors.New("dataset not found")
)

func defaultBackoffStrategy() *backoff.ExponentialBackOff {
	return &backoff.ExponentialBackOff{
		InitialInterval:     100 * time.Millisecond,
		RandomizationFactor: 0.5,
		Multiplier:          1.5,
		MaxInterval:         1 * time.Second,
	}
}

type Transport struct {
	httpClient     *http.Client
	defaultHeaders map[string]string
	site           string
	agentURL       *url.URL
	agentless      bool
	appKey         string
	testBaseURL    string // overrides all URL construction when non-empty
}

// New builds a new Transport for LLM Observability endpoints.
func New(cfg *config.Config) *Transport {
	site := defaultSite
	if cfg.TracerConfig.Site != "" {
		site = cfg.TracerConfig.Site
	}

	defaultHeaders := make(map[string]string)
	if cfg.ResolvedAgentlessEnabled {
		defaultHeaders["DD-API-KEY"] = cfg.TracerConfig.APIKey
	}

	// Clone the HTTP client and remove its global timeout
	// We manage timeouts per-request using context.WithTimeout
	httpClient := cfg.TracerConfig.HTTPClient
	if httpClient != nil && httpClient.Timeout > 0 {
		clientCopy := *httpClient
		clientCopy.Timeout = 0
		httpClient = &clientCopy
	}

	return &Transport{
		httpClient:     httpClient,
		defaultHeaders: defaultHeaders,
		site:           site,
		agentURL:       cfg.TracerConfig.AgentURL,
		agentless:      cfg.ResolvedAgentlessEnabled,
		appKey:         cfg.TracerConfig.APPKey,
		testBaseURL:    cfg.TestBaseURL,
	}
}

// AnyPtr returns a pointer to the given value. This is used to create payloads that require pointers instead of values.
func AnyPtr[T any](v T) *T {
	return &v
}

// NewErrorMessage returns the payload representation of an error.
func NewErrorMessage(err error) *ErrorMessage {
	if err == nil {
		return nil
	}
	return &ErrorMessage{
		Message: err.Error(),
		Type:    errType(err),
		Stack:   errStackTrace(err),
	}
}

func errType(err error) string {
	var originalErr error
	var wErr *errortrace.TracerError
	if !errors.As(err, &wErr) {
		originalErr = err
	} else {
		originalErr = wErr.Unwrap()
	}
	return reflect.TypeOf(originalErr).String()
}

func errStackTrace(err error) string {
	var wErr *errortrace.TracerError
	if !errors.As(err, &wErr) {
		return ""
	}
	return wErr.Format()
}

func (c *Transport) baseURL(subdomain string) string {
	if c.testBaseURL != "" {
		return c.testBaseURL
	}
	if c.agentless {
		return fmt.Sprintf("https://%s.%s", subdomain, c.site)
	}
	u := ""
	if c.agentURL.Scheme == "unix" {
		u = internal.UnixDataSocketURL(c.agentURL.Path).String()
	} else {
		u = c.agentURL.String()
	}
	u += endpointPrefixEVPProxy
	return u
}

// encodeJSON encodes v with HTML escaping disabled, so LLM input/output content
// reaches the intake unmangled, and returns the encoder's buffer (which ends in
// the newline json.Encoder appends).
func encodeJSON(v any) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return &buf, nil
}

// MarshalJSON is encodeJSON without the trailing newline, for callers that need
// the exact encoded size.
func MarshalJSON(v any) ([]byte, error) {
	buf, err := encodeJSON(v)
	if err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func (c *Transport) jsonRequest(ctx context.Context, method, path, subdomain string, body any, timeout time.Duration) (requestResult, error) {
	var jsonBody io.Reader
	if body != nil {
		buf, err := encodeJSON(body)
		if err != nil {
			return requestResult{}, fmt.Errorf("failed to json encode body: %w", err)
		}
		jsonBody = bytes.NewReader(buf.Bytes())
	}
	return c.request(ctx, method, path, subdomain, jsonBody, "application/json", timeout)
}

type requestResult struct {
	statusCode int
	attempts   int
	body       []byte
	retriable  bool
}

// newRequest builds an HTTP request to url under subdomain with the transport's
// content type, default headers, and (in Agent mode) the EVP subdomain header.
func (c *Transport) newRequest(ctx context.Context, method, url, subdomain, contentType string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	for key, val := range c.defaultHeaders {
		req.Header.Set(key, val)
	}
	if !c.agentless {
		req.Header.Set(headerEVPSubdomain, subdomain)
	}
	return req, nil
}

func (c *Transport) request(ctx context.Context, method, path, subdomain string, body io.Reader, contentType string, timeout time.Duration) (requestResult, error) {
	if timeout == 0 {
		timeout = defaultTimeout
	}
	urlStr := c.baseURL(subdomain) + path
	backoffStrat := defaultBackoffStrategy()
	var attempts int
	var last requestResult

	doRequest := func() (result requestResult, err error) {
		attempts++
		log.Debug("llmobs: sending request (method: %s | url: %s)", method, urlStr)
		defer func() {
			if err != nil {
				log.Debug("llmobs: request failed: %s", err.Error())
			}
		}()

		// Reset body reader if it's seekable (for retries)
		if body != nil {
			if seeker, ok := body.(io.Seeker); ok {
				if _, err := seeker.Seek(0, io.SeekStart); err != nil {
					return requestResult{}, fmt.Errorf("failed to reset body reader: %w", err)
				}
			}
		}

		req, err := c.newRequest(ctx, method, urlStr, subdomain, contentType, body)
		if err != nil {
			return requestResult{}, err
		}

		// Set headers for datasets and experiments endpoints (both unstable and stable v2 paths)
		if strings.HasPrefix(path, endpointPrefixDNE) || strings.HasPrefix(path, endpointPrefixDNEStable) {
			if c.agentless && c.appKey != "" {
				// In agentless mode, set the app key header if available
				req.Header.Set("DD-APPLICATION-KEY", c.appKey)
			} else if !c.agentless {
				// In agent mode, always set the NeedsAppKey header (app key is ignored)
				req.Header.Set("X-Datadog-NeedsAppKey", "true")
			}
		}

		// Set per-endpoint timeout
		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		req = req.WithContext(timeoutCtx)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			last = requestResult{attempts: attempts, retriable: ctx.Err() == nil}
			return requestResult{}, err
		}
		defer resp.Body.Close()

		code := resp.StatusCode
		if code >= 200 && code <= 299 {
			b, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				return requestResult{}, fmt.Errorf("failed to read response body: %w", readErr)
			}
			last = requestResult{statusCode: code, attempts: attempts, body: b}
			return last, nil
		}
		if isRetriableStatus(code) {
			body := readResponseBody(resp.Body)
			last = requestResult{
				statusCode: code,
				attempts:   attempts,
				body:       body,
				retriable:  true,
			}
			errMsg := fmt.Sprintf("request failed with transient http status code: %d", code)
			if message := errorBodyMessage(resp.Header, body); message != "" {
				errMsg = fmt.Sprintf("%s: %s", errMsg, message)
			}
			return requestResult{}, fmt.Errorf("%s", errMsg)
		}
		if code == http.StatusTooManyRequests {
			last = requestResult{
				statusCode: code,
				attempts:   attempts,
				body:       readResponseBody(resp.Body),
				retriable:  true,
			}
			wait := parseRetryAfter(resp.Header)
			log.Debug("llmobs: status code 429, waiting %s before retry...", wait.String())
			return requestResult{}, backoff.RetryAfter(int(wait.Seconds()))
		}
		body := readResponseBody(resp.Body)
		last = requestResult{statusCode: code, attempts: attempts, body: body}
		errMsg := fmt.Sprintf("request failed with http status code: %d", resp.StatusCode)
		if message := errorBodyMessage(resp.Header, body); message != "" {
			errMsg = fmt.Sprintf("%s: %s", errMsg, message)
		}
		return requestResult{}, backoff.Permanent(fmt.Errorf("%s", errMsg))
	}

	result, err := backoff.Retry(ctx, doRequest, backoff.WithBackOff(backoffStrat), backoff.WithMaxTries(defaultMaxRetries))
	if err != nil {
		last.attempts = attempts
		if ctx.Err() != nil {
			last.retriable = false
		}
		return last, err
	}
	result.attempts = attempts
	return result, nil
}

// RequestResult reports the outcome of an LLM Obs transport request.
type RequestResult struct {
	StatusCode int
	Attempts   int
	Body       []byte
	Retriable  bool
}

func summarizeRequest(result requestResult) RequestResult {
	return RequestResult{
		StatusCode: result.statusCode,
		Attempts:   result.attempts,
		Body:       result.body,
		Retriable:  result.retriable,
	}
}

func errorBodyMessage(header http.Header, body []byte) string {
	contentType := header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		return ""
	}
	return strings.TrimSpace(string(body))
}

func readResponseBody(body io.Reader) []byte {
	if body == nil {
		return nil
	}
	response, _ := io.ReadAll(io.LimitReader(body, 1<<20))
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<20))
	return response
}

// parseRetryAfter reports how long to wait before retrying a rate-limited
// request, from the Datadog-specific x-ratelimit-reset header.
func parseRetryAfter(h http.Header) time.Duration {
	rateLimitReset := h.Get(headerRateLimitReset)
	waitSeconds := int64(1)
	if rateLimitReset != "" {
		if resetTime, err := strconv.ParseInt(rateLimitReset, 10, 64); err == nil {
			seconds := int64(0)
			if resetTime > time.Now().Unix() {
				// Assume it's a Unix timestamp
				seconds = int64(time.Until(time.Unix(resetTime, 0)).Seconds())
			} else {
				// Assume it's a duration in seconds
				seconds = resetTime
			}
			if seconds > 0 {
				waitSeconds = seconds
			}
		}
	}
	return time.Duration(waitSeconds) * time.Second
}

func isRetriableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout,
		http.StatusTooEarly:
		return true
	}
	if code >= 500 && code <= 599 {
		return true
	}
	return false
}
