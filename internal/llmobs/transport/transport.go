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
	headerRetryAfter     = "Retry-After"
)

const (
	endpointEvalMetric = "/api/intake/llm-obs/v2/eval-metric"
	endpointLLMSpan    = "/api/v2/llmobs"

	endpointPrefixEVPProxy  = "/evp_proxy/v2"
	endpointPrefixDNE       = "/api/unstable/llm-obs/v1"
	endpointPrefixDNEStable = "/api/v2/llm-obs/v1"

	subdomainLLMSpan    = "llmobs-intake"
	subdomainEvalMetric = "api"
	subdomainDNE        = "api"
)

// Exported endpoint paths and EVP subdomains for offline export clients
// (see llmobs/export). They identify the LLM Obs span and evaluation intakes.
const (
	PathLLMSpans      = endpointLLMSpan
	SubdomainLLMSpans = subdomainLLMSpan
	PathEvalMetrics   = endpointEvalMetric
	SubdomainEval     = subdomainEvalMetric
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

func (c *Transport) jsonRequest(ctx context.Context, method, path, subdomain string, body any, timeout time.Duration) (requestResult, error) {
	var jsonBody io.Reader
	if body != nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(body); err != nil {
			return requestResult{}, fmt.Errorf("failed to json encode body: %w", err)
		}
		jsonBody = bytes.NewReader(buf.Bytes())
	}
	return c.request(ctx, method, path, subdomain, jsonBody, "application/json", timeout)
}

type requestResult struct {
	statusCode int
	body       []byte
}

func (c *Transport) request(ctx context.Context, method, path, subdomain string, body io.Reader, contentType string, timeout time.Duration) (requestResult, error) {
	if timeout == 0 {
		timeout = defaultTimeout
	}
	urlStr := c.baseURL(subdomain) + path
	backoffStrat := defaultBackoffStrategy()

	doRequest := func() (result requestResult, err error) {
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

		req, err := http.NewRequestWithContext(ctx, method, urlStr, body)
		if err != nil {
			return requestResult{}, err
		}

		req.Header.Set("Content-Type", contentType)
		for key, val := range c.defaultHeaders {
			req.Header.Set(key, val)
		}
		if !c.agentless {
			req.Header.Set(headerEVPSubdomain, subdomain)
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
			return requestResult{}, err
		}
		defer resp.Body.Close()

		code := resp.StatusCode
		if code >= 200 && code <= 299 {
			b, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				return requestResult{}, fmt.Errorf("failed to read response body: %w", readErr)
			}
			return requestResult{statusCode: code, body: b}, nil
		}
		if isRetriableStatus(code) {
			errMsg := fmt.Sprintf("request failed with transient http status code: %d", code)
			if body := readErrorBody(resp); body != "" {
				errMsg = fmt.Sprintf("%s: %s", errMsg, body)
			}
			return requestResult{}, fmt.Errorf("%s", errMsg)
		}
		if code == http.StatusTooManyRequests {
			wait := parseRetryAfter(resp.Header)
			log.Debug("llmobs: status code 429, waiting %s before retry...", wait.String())
			drainAndClose(resp.Body)
			return requestResult{}, backoff.RetryAfter(int(wait.Seconds()))
		}
		errMsg := fmt.Sprintf("request failed with http status code: %d", resp.StatusCode)
		if body := readErrorBody(resp); body != "" {
			errMsg = fmt.Sprintf("%s: %s", errMsg, body)
		}
		drainAndClose(resp.Body)
		return requestResult{}, backoff.Permanent(fmt.Errorf("%s", errMsg))
	}

	return backoff.Retry(ctx, doRequest, backoff.WithBackOff(backoffStrat), backoff.WithMaxTries(defaultMaxRetries))
}

// Result reports the outcome of a single Post performed by an offline export
// client. It surfaces the final HTTP status, the number of attempts made, the
// (bounded) response body, and whether the failure class was retriable.
type Result struct {
	StatusCode int
	Attempts   int
	Body       []byte
	Retriable  bool
}

// Post sends an already-encoded body to path under the given EVP subdomain using
// the transport's routing (agentless direct intake vs. Agent EVP proxy), auth
// headers, and bounded retry policy, and reports a structured Result.
//
// It is intended for offline export clients (llmobs/export) that build their own
// payloads and need per-request outcomes. Retry classification matches the rest
// of the transport (5xx/408/425/429 retriable, other 4xx permanent); callers
// must not layer additional retry on top of it.
func (c *Transport) Post(ctx context.Context, path, subdomain, contentType string, body []byte) (Result, error) {
	urlStr := c.baseURL(subdomain) + path
	backoffStrat := defaultBackoffStrategy()

	var attempts int
	var last Result

	op := func() (Result, error) {
		attempts++
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, urlStr, bytes.NewReader(body))
		if err != nil {
			return Result{}, backoff.Permanent(err)
		}
		req.Header.Set("Content-Type", contentType)
		for key, val := range c.defaultHeaders {
			req.Header.Set(key, val)
		}
		if !c.agentless {
			req.Header.Set(headerEVPSubdomain, subdomain)
		}

		timeoutCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
		defer cancel()
		req = req.WithContext(timeoutCtx)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			// Network/transport error is retriable, but a caller-cancelled or
			// expired parent context is not a transient server condition.
			last = Result{Retriable: ctx.Err() == nil}
			return Result{}, err
		}
		defer resp.Body.Close()

		code := resp.StatusCode
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if code >= 200 && code <= 299 {
			last = Result{StatusCode: code, Body: respBody}
			return last, nil
		}
		if isRetriableStatus(code) || code == http.StatusTooManyRequests {
			last = Result{StatusCode: code, Body: respBody, Retriable: true}
			// Honor a server-advertised delay when present (always for 429; for a
			// retriable 5xx/408 only when the response actually carries the header,
			// e.g. a 503 during throttling/maintenance) instead of the short
			// exponential backoff; otherwise fall back to exponential backoff.
			if code == http.StatusTooManyRequests || hasRetryAfterHeader(resp.Header) {
				return Result{}, backoff.RetryAfter(int(parseRetryAfter(resp.Header).Seconds()))
			}
			return Result{}, fmt.Errorf("request failed with transient http status code: %d", code)
		}
		last = Result{StatusCode: code, Body: respBody}
		return Result{}, backoff.Permanent(fmt.Errorf("request failed with http status code: %d", code))
	}

	res, err := backoff.Retry(ctx, op, backoff.WithBackOff(backoffStrat), backoff.WithMaxTries(defaultMaxRetries))
	if err != nil {
		last.Attempts = attempts
		// A caller-cancelled or expired context is not a transient failure, even if
		// the last recorded response was retriable: clear Retriable so callers using
		// it for outbox/retry decisions don't re-enqueue on a cancellation.
		if ctx.Err() != nil {
			last.Retriable = false
		}
		return last, err
	}
	res.Attempts = attempts
	return res, nil
}

func readErrorBody(resp *http.Response) string {
	if resp == nil || resp.Body == nil {
		return ""
	}
	// Only read the body if it's JSON
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

func drainAndClose(b io.ReadCloser) {
	if b == nil {
		return
	}
	io.Copy(io.Discard, io.LimitReader(b, 1<<20)) // drain up to 1MB to reuse conn
	_ = b.Close()
}

// hasRetryAfterHeader reports whether the response advertises a retry delay via
// the standard Retry-After header or the Datadog-specific x-ratelimit-reset.
func hasRetryAfterHeader(h http.Header) bool {
	return h.Get(headerRetryAfter) != "" || h.Get(headerRateLimitReset) != ""
}

func parseRetryAfter(h http.Header) time.Duration {
	// Honor the standard Retry-After header first (delta-seconds or HTTP-date),
	// as 429/503 responses advertise, before falling back to the Datadog-specific
	// x-ratelimit-reset header and finally a 1s default.
	if ra := strings.TrimSpace(h.Get(headerRetryAfter)); ra != "" {
		if secs, err := strconv.ParseInt(ra, 10, 64); err == nil {
			if secs > 0 {
				return time.Duration(secs) * time.Second
			}
		} else if t, err := http.ParseTime(ra); err == nil {
			if d := time.Until(t); d > 0 {
				return d
			}
		}
	}
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
