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
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/DataDog/dd-trace-go/v2/internal/exportutil"
)

const defaultRequestTimeout = 10 * time.Second

var errNilRequest = errors.New("otlp/export: nil request")

// rawTransport posts protobuf-encoded OTLP payloads to a fixed endpoint with
// bounded retry.
type rawTransport struct {
	client         *http.Client
	endpoint       string
	headers        http.Header
	maxAttempts    uint
	requestTimeout time.Duration // 0 = default only when the caller sets no deadline
}

// newRawTransport resolves the endpoint and headers for a signal and builds a
// transport. signalPath is one of the /v1/<signal> constants; extraHeaders are
// signal-specific headers (e.g. the metric config) applied before Config.Headers.
func newRawTransport(cfg Config, signalPath string, extraHeaders map[string]string) (*rawTransport, error) {
	base := cfg.Endpoint
	if base == "" {
		site := cfg.Site
		if site == "" {
			site = defaultSite
		}
		if cfg.APIKey == "" {
			return nil, errors.New("otlp/export: APIKey is required for the Datadog OTLP route; set Endpoint to use a collector/Agent")
		}
		base = "https://otlp." + site
	}
	if u, err := url.Parse(base); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("otlp/export: invalid endpoint %q: must be an http(s) URL with a host (OTLP/gRPC is not supported)", base)
	}
	endpoint := strings.TrimRight(base, "/") + signalPath

	// Assemble on an http.Header so keys are canonicalized and later layers
	// deterministically override earlier ones regardless of caller casing.
	headers := http.Header{}
	headers.Set(headerContentType, contentTypeProto)
	if cfg.APIKey != "" {
		headers.Set(headerAPIKey, cfg.APIKey)
	}
	for k, v := range extraHeaders {
		headers.Set(k, v)
	}
	for k, v := range cfg.Headers {
		headers.Set(k, v)
	}

	client := cfg.HTTPClient
	if client == nil {
		client = defaultHTTPClient()
	} else if client.CheckRedirect == nil {
		// Enforce no-redirect even on a caller-provided client that does not set its
		// own policy: following a redirect would drop the POST body (a lost export
		// reported as success) and forward the dd-api-key header to the redirect
		// target. Copy the client so the caller's value is not mutated.
		cp := *client
		cp.CheckRedirect = noRedirect
		client = &cp
	}
	maxAttempts := cfg.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = defaultMaxAttempts
	}
	return &rawTransport{client: client, endpoint: endpoint, headers: headers, maxAttempts: maxAttempts, requestTimeout: cfg.RequestTimeout}, nil
}

// export marshals msg and POSTs it with bounded retry, returning a RequestResult
// and the raw response body (for partial-success decoding by the caller).
func (t *rawTransport) export(ctx context.Context, msg proto.Message) (RequestResult, []byte) {
	var rr RequestResult
	body, err := proto.Marshal(msg)
	if err != nil {
		rr.Err = fmt.Errorf("otlp/export: marshal: %w", err)
		return rr, nil
	}
	res, err := exportutil.Retry(ctx, exportutil.RetryOptions{
		MaxAttempts: t.maxAttempts,
		Retriable:   otlpRetriable,
	}, func(ctx context.Context) exportutil.Attempt {
		return t.doPost(ctx, body)
	})
	rr.StatusCode = res.StatusCode
	rr.Attempts = res.Attempts
	rr.Retriable = res.Retriable
	rr.Err = err
	// On failure an OTLP/HTTP endpoint returns a google.rpc.Status protobuf;
	// surface its message rather than raw protobuf control bytes, falling back to
	// the raw body when it is not a decodable Status. Either way the result runs
	// through Snippet so ResponseSnippet stays bounded and UTF-8-safe.
	if statusMsg := decodedStatusSnippet(err, res.Body); statusMsg != "" {
		rr.ResponseSnippet = statusMsg
	} else {
		rr.ResponseSnippet = exportutil.Snippet(res.Body)
	}
	return rr, res.Body
}

// decodedStatusSnippet returns a bounded snippet of the google.rpc.Status message
// for a failed request, or "" to fall back to a raw-body snippet.
func decodedStatusSnippet(err error, body []byte) string {
	if err == nil {
		return ""
	}
	return exportutil.Snippet([]byte(otlpStatusMessage(body)))
}

func (t *rawTransport) doPost(ctx context.Context, body []byte) exportutil.Attempt {
	// Apply a per-request timeout. An explicit Config.RequestTimeout always wins;
	// otherwise fall back to the default only when the caller's context carries no
	// deadline of its own, so a caller that passes a longer deadline is not
	// silently shortened.
	if t.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.requestTimeout)
		defer cancel()
	} else if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultRequestTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(body))
	if err != nil {
		return exportutil.Attempt{Err: err}
	}
	req.Header = t.headers.Clone()

	resp, err := t.client.Do(req)
	if err != nil {
		return exportutil.Attempt{Err: err}
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	// OTLP/HTTP defines success as exactly 200 OK with a protobuf Export*Response
	// body. Treat any other 2xx (202/204/206, or a redirect not followed) as a
	// failed export rather than silently reporting delivery; the body decode is
	// validated by the caller's partial-success decoder.
	if resp.StatusCode == http.StatusOK {
		if readErr != nil {
			// The 200 body carries OTLP partial-success rejections; a failed read
			// could hide dropped records, so surface it as a transport-class
			// (retryable, status 0) failure instead of reporting full success.
			return exportutil.Attempt{Body: respBody, Err: fmt.Errorf("otlp/export: read response body: %w", readErr)}
		}
		return exportutil.Attempt{Status: resp.StatusCode, Body: respBody}
	}
	return exportutil.Attempt{
		Status:     resp.StatusCode,
		Body:       respBody,
		RetryAfter: parseRetryAfter(resp.Header),
		Err:        fmt.Errorf("otlp/export: unexpected status %d", resp.StatusCode),
	}
}

// otlpRetriable applies the OTLP/HTTP retryable-status rules: only 429, 502, 503
// and 504 (plus network-level errors, status 0) are retried; every other 4xx/5xx
// is permanent. See
// https://opentelemetry.io/docs/specs/otlp/#retryable-response-codes.
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

const maxRetryAfter = 60 * time.Second

// parseRetryAfter reads a Retry-After header (delta-seconds or HTTP-date) and
// returns the delay to wait, clamped to (0, maxRetryAfter]. It returns 0 when the
// header is absent or unparseable so the caller falls back to backoff.
func parseRetryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		if secs <= 0 {
			return 0
		}
		// Parse as int64 (not int, which is 32-bit on some builds) and clamp the
		// second-count before converting, so a large hint (e.g. "9223372037") is
		// capped rather than overflowing time.Duration into a non-positive value.
		if secs > int64(maxRetryAfter/time.Second) {
			return maxRetryAfter
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return clampRetryAfter(time.Until(t))
	}
	return 0
}

func clampRetryAfter(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return 0
	case d > maxRetryAfter:
		return maxRetryAfter
	default:
		return d
	}
}

// otlpStatusMessage best-effort extracts the human-readable message from a
// google.rpc.Status protobuf body (field 2, a string). It returns "" when body
// is not a decodable Status, without pulling in the generated Status type.
func otlpStatusMessage(body []byte) string {
	b := body
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return ""
		}
		b = b[n:]
		if num == 2 && typ == protowire.BytesType {
			v, vn := protowire.ConsumeBytes(b)
			if vn < 0 {
				return ""
			}
			return string(v)
		}
		skip := protowire.ConsumeFieldValue(num, typ, b)
		if skip < 0 {
			return ""
		}
		b = b[skip:]
	}
	return ""
}

// noRedirect stops the HTTP client from following redirects: Go would replay the
// POST as a GET with the body dropped (a lost export reported as success) and
// forward the dd-api-key header to the redirect target (a credential leak).
// Surfacing the 3xx as a non-200 response makes it a failed export instead.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func defaultHTTPClient() *http.Client {
	return &http.Client{
		CheckRedirect: noRedirect,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}
