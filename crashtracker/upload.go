// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
)

// uploadReport sends a crash report to the Error Tracking intake.
func uploadReport(cfg *config, r *Report) error {
	body, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("crashtracker: marshal report: %w", err)
	}

	req, client, err := buildRequestAndClient(cfg, body)
	if err != nil {
		return fmt.Errorf("crashtracker: build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("crashtracker: send report: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("crashtracker: intake returned %d", resp.StatusCode)
	}
	return nil
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
		// Agentless path.
		// cfg.agentURL acts as a base URL override for testing; in production it is empty.
		base := cfg.agentURL
		if base == "" {
			site := cfg.site
			if site == "" {
				site = defaultSite
			}
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
			targetURL = base + agentEVPPath
		}
	}

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
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
