// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
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

	uploadTimeout = 10 * time.Second

	// uploadAttempts bounds how many times uploadReport tries to deliver a
	// report before giving up. A crash report is a strictly one-shot payload:
	// the monitor calls os.Exit(0) immediately after, with no later flush and
	// no buffer to fall back on, so a single transient failure (the Agent
	// restarting, a 503, a reset connection) would otherwise lose the report
	// permanently.
	uploadAttempts = 3

	// uploadRetryBackoff is the base delay between upload attempts, scaled
	// linearly by attempt number (500ms, 1s).
	uploadRetryBackoff = 500 * time.Millisecond
)

// uploadReport sends a crash report to the Error Tracking intake, retrying
// transient failures.
func uploadReport(cfg *config, r *Report) error {
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

	var lastErr error
	for attempt := range uploadAttempts {
		if attempt > 0 {
			time.Sleep(uploadRetryBackoff * time.Duration(attempt))
		}
		retryable, err := attemptUpload(cfg, compressed)
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
// worth retrying. buildRequestAndClient is called fresh on every attempt
// (rather than reusing one *http.Request) specifically so the request body
// is rebuilt each time: an http.Request's body reader is consumed after use
// and cannot simply be replayed on a retry.
func attemptUpload(cfg *config, compressed []byte) (retryable bool, err error) {
	req, client, err := buildRequestAndClient(cfg, compressed)
	if err != nil {
		return false, fmt.Errorf("crashtracker: build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		// A transport-level failure (connection reset, DNS hiccup, the Agent
		// mid-restart) might succeed on a later attempt.
		return true, fmt.Errorf("crashtracker: send report: %w", err)
	}
	defer resp.Body.Close()

	// Anything outside 2xx is a failed upload, not just 4xx/5xx: a 3xx that
	// client.Do did not (or could not) follow means the report was never
	// actually accepted by the intake, and this is the only check standing
	// between that and silently treating the upload as successful.
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// 5xx and 429 are worth retrying; any other 4xx (bad request, invalid
		// API key, ...) will fail identically on every attempt.
		retryable := resp.StatusCode >= http.StatusInternalServerError || resp.StatusCode == http.StatusTooManyRequests
		return retryable, fmt.Errorf("crashtracker: intake returned %d", resp.StatusCode)
	}
	return false, nil
}

// buildRequestAndClient builds an HTTP request and the matching client.
// For Unix socket agent URLs it returns a UDS-aware client and rewrites the
// request URL to http://localhost so net/http can POST through the socket.
func buildRequestAndClient(cfg *config, body []byte) (*http.Request, *http.Client, error) {
	var (
		targetURL  string
		useKey     bool
		socketPath string
	)

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
		targetURL = base
		useKey = true
	} else {
		// Agent EVP proxy path.
		base := cfg.agentURL
		if base == "" {
			base = internal.AgentURLFromEnv().String()
		}
		// Detect Unix socket agent URLs: use http://localhost for the request
		// and dial the socket directly via UDSClient.
		if u, err := url.Parse(base); err == nil && u.Scheme == "unix" {
			socketPath = u.Path
			targetURL = "http://localhost" + agentEVPPath
		} else {
			// Trim any trailing slash to avoid double-slash in the path.
			targetURL = strings.TrimRight(base, "/") + agentEVPPath
		}
	}

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	if useKey {
		req.Header.Set("DD-API-KEY", cfg.apiKey)
	} else {
		req.Header.Set("X-Datadog-EVP-Subdomain", agentEVPSubdomain)
	}

	var client *http.Client
	switch {
	case cfg.httpClient != nil:
		client = cfg.httpClient
	case socketPath != "":
		client = internal.UDSClient(socketPath, uploadTimeout)
	default:
		client = internal.DefaultHTTPClient(uploadTimeout, false)
	}
	return req, client, nil
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
