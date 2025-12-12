// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// Environment variable names for flag rules backend configuration
const (
	envFlagRulesURL          = "DD_FFE_FLAG_RULES_URL"
	envFlagRulesPollInterval = "DD_FFE_FLAG_RULES_POLL_INTERVAL_SECONDS"

	defaultFlagRulesPollInterval = 30 * time.Second
	defaultFlagRulesTimeout      = 10 * time.Second
)

// FlagRulesConfig holds configuration for the flag rules backend data source.
type FlagRulesConfig struct {
	// URL is the HTTP endpoint URL for fetching flag configurations.
	// Can also be set via DD_FFE_FLAG_RULES_URL environment variable.
	// Constructor option takes precedence over environment variable.
	URL string

	// PollInterval is the interval between polling requests.
	// Can also be set via DD_FFE_FLAG_RULES_POLL_INTERVAL_SECONDS environment variable.
	// Constructor option takes precedence over environment variable.
	// Default: 30 seconds
	PollInterval time.Duration

	// HTTPClient is the HTTP client to use for requests.
	// If nil, a default client with reasonable timeouts is created.
	HTTPClient *http.Client
}

// flagRulesBackend implements the flag rules backend data source for OpenFeature.
type flagRulesBackend struct {
	config   FlagRulesConfig
	provider *DatadogProvider
	client   *http.Client

	mu           sync.RWMutex
	lastETag     string
	lastModified string

	stopCh   chan struct{}
	stopOnce sync.Once
}

// newFlagRulesBackend creates a new flag rules backend with the given configuration.
func newFlagRulesBackend(config FlagRulesConfig, provider *DatadogProvider) (*flagRulesBackend, error) {
	// Resolve URL from config or environment variable (config takes precedence)
	url := config.URL
	if url == "" {
		url = os.Getenv(envFlagRulesURL)
	}
	if url == "" {
		return nil, fmt.Errorf("flag rules URL is required: set via ProviderConfig.FlagRules.URL or %s environment variable", envFlagRulesURL)
	}

	// Resolve poll interval from config or environment variable (config takes precedence)
	pollInterval := config.PollInterval
	if pollInterval == 0 {
		if intervalStr := os.Getenv(envFlagRulesPollInterval); intervalStr != "" {
			if intervalSec, err := strconv.ParseFloat(intervalStr, 64); err == nil && intervalSec > 0 {
				pollInterval = time.Duration(intervalSec * float64(time.Second))
			} else {
				log.Debug("openfeature/flagrules: invalid poll interval %q, using default", intervalStr)
			}
		}
		if pollInterval == 0 {
			pollInterval = defaultFlagRulesPollInterval
		}
	}

	// Use provided HTTP client or create default
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = createDefaultFlagRulesHTTPClient()
	}

	return &flagRulesBackend{
		config: FlagRulesConfig{
			URL:          url,
			PollInterval: pollInterval,
			HTTPClient:   httpClient,
		},
		provider: provider,
		client:   httpClient,
		stopCh:   make(chan struct{}),
	}, nil
}

// createDefaultFlagRulesHTTPClient creates an HTTP client optimized for polling.
func createDefaultFlagRulesHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultFlagRulesTimeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// Start begins the polling loop.
func (f *flagRulesBackend) Start() {
	log.Debug("openfeature/flagrules: starting flag rules backend [url: %s, interval: %s]", f.config.URL, f.config.PollInterval)

	// Perform initial fetch immediately
	f.fetchConfiguration()

	// Start polling loop
	go f.pollLoop()
}

// pollLoop runs the periodic polling for configuration updates.
func (f *flagRulesBackend) pollLoop() {
	ticker := time.NewTicker(f.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-f.stopCh:
			log.Debug("openfeature/flagrules: polling loop stopped")
			return
		case <-ticker.C:
			f.fetchConfiguration()
		}
	}
}

// fetchConfiguration fetches the configuration from the backend.
func (f *flagRulesBackend) fetchConfiguration() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultFlagRulesTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.config.URL, nil)
	if err != nil {
		log.Error("openfeature/flagrules: failed to create request: %v", err)
		return
	}

	// Set conditional headers to avoid re-downloading unchanged data
	f.mu.RLock()
	if f.lastETag != "" {
		req.Header.Set("If-None-Match", f.lastETag)
	}
	if f.lastModified != "" {
		req.Header.Set("If-Modified-Since", f.lastModified)
	}
	f.mu.RUnlock()

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := f.client.Do(req)
	if err != nil {
		log.Debug("openfeature/flagrules: request failed: %v", err)
		return
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	// Handle 304 Not Modified - configuration unchanged
	if resp.StatusCode == http.StatusNotModified {
		log.Debug("openfeature/flagrules: configuration unchanged (304)")
		return
	}

	// Handle non-success status codes
	if resp.StatusCode != http.StatusOK {
		log.Debug("openfeature/flagrules: unexpected status code: %d", resp.StatusCode)
		return
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error("openfeature/flagrules: failed to read response body: %v", err)
		return
	}

	// Parse configuration
	var config universalFlagsConfiguration
	if err := json.Unmarshal(body, &config); err != nil {
		log.Error("openfeature/flagrules: failed to parse configuration: %v", err)
		return
	}

	// Validate configuration (reuse validation from remoteconfig.go)
	if err := validateConfiguration(&config); err != nil {
		log.Error("openfeature/flagrules: invalid configuration: %v", err)
		return
	}

	// Update provider with new configuration
	f.provider.updateConfiguration(&config)
	log.Debug("openfeature/flagrules: successfully applied configuration with %d flags", len(config.Flags))

	// Store conditional headers for next request
	f.mu.Lock()
	f.lastETag = resp.Header.Get("ETag")
	f.lastModified = resp.Header.Get("Last-Modified")
	f.mu.Unlock()
}

// Stop stops the polling loop.
func (f *flagRulesBackend) Stop() error {
	f.stopOnce.Do(func() {
		log.Debug("openfeature/flagrules: stopping flag rules backend")
		close(f.stopCh)
	})
	return nil
}
