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

	"github.com/DataDog/dd-trace-go/v2/instrumentation/errortrace"
	"github.com/cenkalti/backoff/v5"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

const (
	headerEVPSubdomain   = "X-Datadog-EVP-Subdomain"
	headerRateLimitReset = "x-ratelimit-reset"
)

const (
	endpointEvalMetric = "/api/intake/llm-obs/v2/eval-metric"
	endpointLLMSpan    = "/api/v2/llmobs"

	endpointPrefixEVPProxy = "/evp_proxy/v2"
	endpointPrefixDNE      = "/api/unstable/llm-obs/v1"

	subdomainLLMSpan    = "llmobs-intake"
	subdomainEvalMetric = "api"
	subdomainDNE        = "api"
)

const (
	defaultSite                     = "datadoghq.com"
	defaultMaxRetries uint          = 3
	defaultBackoff    time.Duration = 100 * time.Millisecond
)

var (
	ErrDatasetNotFound = errors.New("dataset not found")
)

type Transport struct {
	httpClient     *http.Client
	defaultHeaders map[string]string
	site           string
	agentURL       *url.URL
	agentless      bool
	appKey         string
}

// New builds a new Transport for LLM Observability endpoints.
func New(cfg *config.Config) *Transport {
	site := defaultSite
	if cfg.TracerConfig.Site != "" {
		site = cfg.TracerConfig.Site
	}

	defaultHeaders := map[string]string{
		"Content-Type": "application/json",
	}
	if cfg.ResolvedAgentlessEnabled {
		defaultHeaders["DD-API-KEY"] = cfg.TracerConfig.APIKey
	}
	return &Transport{
		httpClient:     cfg.TracerConfig.HTTPClient,
		defaultHeaders: defaultHeaders,
		site:           site,
		agentURL:       cfg.TracerConfig.AgentURL,
		agentless:      cfg.ResolvedAgentlessEnabled,
		appKey:         cfg.TracerConfig.APPKey,
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

func (c *Transport) request(ctx context.Context, method, path, subdomain string, body any) (int, []byte, error) {
	urlStr := c.baseURL(subdomain) + path

	var reqBody io.Reader
	if body != nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(body); err != nil {
			return 0, nil, fmt.Errorf("encode body: %w", err)
		}
		reqBody = bytes.NewReader(buf.Bytes())
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, reqBody)
	if err != nil {
		return 0, nil, err
	}

	for key, val := range c.defaultHeaders {
		req.Header.Set(key, val)
	}
	if !c.agentless {
		req.Header.Set(headerEVPSubdomain, subdomain)
	}

	// Set headers for datasets and experiments endpoints
	if strings.HasPrefix(path, endpointPrefixDNE) {
		if c.agentless && c.appKey != "" {
			// In agentless mode, set the app key header if available
			req.Header.Set("DD-APPLICATION-KEY", c.appKey)
		} else if !c.agentless {
			// In agent mode, always set the NeedsAppKey header (app key is ignored)
			req.Header.Set("X-Datadog-NeedsAppKey", "true")
		}
	}
	backoffStrat := &backoff.ExponentialBackOff{
		InitialInterval:     defaultBackoff,
		RandomizationFactor: 0.5,
		Multiplier:          1.5,
		MaxInterval:         1 * time.Second,
	}

	doRequest := func() (resp *http.Response, err error) {
		resp, err = c.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer func() {
			if err != nil && resp != nil {
				_ = resp.Body.Close()
			}
		}()

		code := resp.StatusCode

		if code >= 200 && code <= 299 {
			return resp, nil
		}

		if isRetriableStatus(code) {
			log.Debug("llmobs/internal/transport: retriable status code: %d", resp.StatusCode)
			return nil, fmt.Errorf("request failed with transient http status code: %d", code)
		}

		if code == http.StatusTooManyRequests {
			wait := parseRetryAfter(resp.Header)
			log.Debug("llmobs/internal/transport: status code 429, waiting %s before retry...", wait.String())
			drainAndClose(resp.Body)
			return nil, backoff.RetryAfter(int(wait.Seconds()))
		}

		log.Debug("llmobs/internal/transport: non-retriable status code: %d", resp.StatusCode)
		drainAndClose(resp.Body)
		return nil, backoff.Permanent(fmt.Errorf("request failed with http status code: %d", resp.StatusCode))
	}

	log.Debug("llmobs/internal/transport: sending request (method: %s | url: %s)", method, urlStr)

	resp, err := backoff.Retry(ctx, doRequest, backoff.WithBackOff(backoffStrat), backoff.WithMaxTries(defaultMaxRetries))
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	log.Debug("llmobs/internal/transport: got success response body: %s", string(b))

	return resp.StatusCode, b, nil
}

func drainAndClose(b io.ReadCloser) {
	if b == nil {
		return
	}
	io.Copy(io.Discard, io.LimitReader(b, 1<<20)) // drain up to 1MB to reuse conn
	_ = b.Close()
}

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
