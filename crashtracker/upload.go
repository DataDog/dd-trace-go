// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	internal "github.com/DataDog/dd-trace-go/v2/internal"
)

const (
	// Error Tracking intake routing, matching libdatadog's crashtracker
	// (DataDog/libdatadog libdd-crashtracker/src/crash_info/errors_intake.rs, RFC 0013).
	agentEVPPath      = "/evp_proxy/v4/api/v2/errorsintake"
	agentEVPSubdomain = "error-tracking-intake"

	agentlessURLTemplate = "https://error-tracking-intake.%s/api/v2/errorsintake"
	defaultSite          = "datadoghq.com"

	// uploadAttempts bounds how many times uploadReport tries to deliver a
	// report before giving up. A crash report is a strictly one-shot payload:
	// the supervisor exits immediately after, with no later flush and
	// no buffer to fall back on, so a single transient failure (the Agent
	// restarting, a 503, a reset connection) would otherwise lose the report
	// permanently.
	uploadAttempts = 3

	// uploadRetryBackoff is the base delay between upload attempts, scaled
	// linearly by attempt number (500ms, 1s).
	uploadRetryBackoff = 500 * time.Millisecond

	// maxDrainedResponseBytes bounds how much of a response body attemptUpload
	// reads before it closes the body. Reading the body to EOF lets the
	// transport reuse the connection for the next attempt.
	maxDrainedResponseBytes = 64 * 1024
)

// uploadReport sends a crash report to the Error Tracking intake, retrying
// transient failures.
func uploadReport(ctx context.Context, cfg *config, r *Report) error {
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("crashtracker: marshal report: %w", err)
	}

	// The parsed report can be several times larger than the raw crash dump
	// (each frame becomes multiple JSON fields); gzip before sending so a
	// large goroutine dump does not turn into an equally large HTTP request.
	compressed, err := gzipCompress(body)
	if err != nil {
		return fmt.Errorf("crashtracker: compress report: %w", err)
	}

	// Build the client once: every attempt reuses its transport and connection.
	target := resolveUploadTarget(cfg)
	client := target.newClient(cfg)

	var lastErr error
	for attempt := range uploadAttempts {
		if attempt > 0 {
			timer := time.NewTimer(uploadRetryBackoff * time.Duration(attempt))
			select {
			case <-ctx.Done():
				timer.Stop()
				if lastErr != nil {
					return fmt.Errorf("crashtracker: upload deadline: %w (last error: %v)", ctx.Err(), lastErr)
				}
				return fmt.Errorf("crashtracker: upload deadline: %w", ctx.Err())
			case <-timer.C:
			}
		}
		retryable, err := attemptUpload(ctx, client, target, cfg, compressed)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable {
			return err
		}
	}
	return fmt.Errorf("crashtracker: upload failed after %d attempts: %w", uploadAttempts, lastErr)
}

// attemptUpload makes one upload attempt and reports whether a failure is
// worth retrying. The request is built fresh on every attempt (rather than
// reusing one *http.Request) specifically so the request body is rebuilt each
// time: an http.Request's body reader is consumed after use and cannot simply
// be replayed on a retry. The client is shared across attempts.
func attemptUpload(ctx context.Context, client *http.Client, target uploadTarget, cfg *config, compressed []byte) (retryable bool, err error) {
	req, err := target.newRequest(cfg, compressed)
	if err != nil {
		return false, fmt.Errorf("crashtracker: build request: %w", err)
	}

	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		// A transport-level failure (connection reset, DNS hiccup, the Agent
		// mid-restart) might succeed on a later attempt.
		return true, fmt.Errorf("crashtracker: send report: %w", err)
	}
	defer func() {
		// Drain a bounded amount so the connection returns to the pool.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainedResponseBytes))
		_ = resp.Body.Close()
	}()

	// Anything outside 2xx is a failed upload, not just 4xx/5xx: a 3xx that
	// client.Do did not (or could not) follow means the report was never
	// actually accepted by the intake, and this is the only check standing
	// between that and silently treating the upload as successful.
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// 5xx, 429, and 408 are worth retrying; any other 4xx (bad request,
		// invalid API key, ...) will fail identically on every attempt. 408
		// specifically means the server gave up waiting for the request, which
		// repeating unmodified is expected to resolve (RFC 7231 §6.5.7).
		retryable := resp.StatusCode >= http.StatusInternalServerError ||
			resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusRequestTimeout
		return retryable, fmt.Errorf("crashtracker: intake returned %d", resp.StatusCode)
	}
	return false, nil
}

// uploadTarget is the resolved destination of an upload.
type uploadTarget struct {
	url        string
	useKey     bool   // authenticate with the API key (agentless) instead of the EVP subdomain header
	socketPath string // non-empty for a Unix socket agent URL
}

// resolveUploadTarget resolves where cfg sends the report. For Unix socket
// agent URLs it rewrites the request URL to http://localhost so net/http can
// POST through the socket.
func resolveUploadTarget(cfg *config) uploadTarget {
	var t uploadTarget
	if cfg.apiKey != "" {
		// Agentless path. cfg.agentlessURL is a test-only override (no public
		// Option); WithAgentURL always means the agent path below, so setting
		// both WithAgentURL and WithAPIKey cannot silently drop the report
		// by treating an agent base URL as a complete agentless target.
		base := cfg.agentlessURL
		if base == "" {
			site := cfg.site
			if site == "" {
				site = defaultSite
			}
			// Tolerate DD_SITE supplied as a URL ("https://datadoghq.com")
			// rather than a bare host, which would otherwise interpolate into
			// a malformed intake URL (host position containing a full URL).
			site = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(site, "https://"), "http://"), "/")
			base = fmt.Sprintf(agentlessURLTemplate, site)
		}
		t.url = base
		t.useKey = true
		return t
	}

	// Agent EVP proxy path.
	base := cfg.agentURL
	if base == "" {
		base = internal.AgentURLFromEnv().String()
	}
	// Detect Unix socket agent URLs: use http://localhost for the request
	// and dial the socket directly via UDSClient.
	if u, err := url.Parse(base); err == nil && u.Scheme == "unix" {
		t.socketPath = u.Path
		t.url = "http://localhost" + agentEVPPath
	} else {
		// Trim any trailing slash to avoid double-slash in the path.
		t.url = strings.TrimRight(base, "/") + agentEVPPath
	}
	return t
}

// newRequest builds an HTTP request that carries body.
func (t uploadTarget) newRequest(cfg *config, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	if t.useKey {
		req.Header.Set("DD-API-KEY", cfg.apiKey)
	} else {
		req.Header.Set("X-Datadog-EVP-Subdomain", agentEVPSubdomain)
	}
	return req, nil
}

// newClient returns the client for the upload: the caller-supplied client from
// WithHTTPClient, a UDS-aware client for a Unix socket agent URL, or the default
// client.
func (t uploadTarget) newClient(cfg *config) *http.Client {
	switch {
	case cfg.httpClient != nil:
		return cfg.httpClient
	case t.socketPath != "":
		client := internal.UDSClient(t.socketPath, attemptTimeout)
		client.CheckRedirect = rejectRedirects
		return client
	default:
		client := internal.DefaultHTTPClient(attemptTimeout, false)
		client.CheckRedirect = rejectRedirects
		return client
	}
}

// rejectRedirects stops net/http from following a 3xx response. The intake
// never legitimately redirects, and the default client silently converts a
// redirected POST to a GET and drops its body for 301/302/303 (net/http's
// documented behavior): if the redirect target then answered 200, attemptUpload
// would report success for a request that never carried the report. Returning
// http.ErrUseLastResponse instead surfaces the original 3xx as resp.StatusCode,
// which attemptUpload's existing non-2xx check already treats as a failure.
// Left unset on a caller-supplied WithHTTPClient, which owns its own redirect
// policy.
func rejectRedirects(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// gzipCompress compresses body for upload.
func gzipCompress(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(body); err != nil {
		return nil, fmt.Errorf("write gzip stream: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close gzip stream: %w", err)
	}
	return buf.Bytes(), nil
}
