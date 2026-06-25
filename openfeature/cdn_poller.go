// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	defaultCDNConfigPath = "/mock/ufc/config"
	cdnAPIKeyHeader      = "DD-API-KEY"

	maxCDNResponseBodyBytes = 10 << 20
)

type cdnPollerConfig struct {
	baseURL        string
	apiKey         string
	pollInterval   time.Duration
	requestTimeout time.Duration
	httpClient     *http.Client
	apply          func(*universalFlagsConfiguration)
	backoff        func(int) time.Duration
}

type cdnPoller struct {
	endpoint       string
	apiKey         string
	pollInterval   time.Duration
	requestTimeout time.Duration
	httpClient     *http.Client
	apply          func(*universalFlagsConfiguration)
	backoff        func(int) time.Duration
	tickC          <-chan time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu   sync.Mutex
	etag string
}

func newCDNPoller(config cdnPollerConfig) (*cdnPoller, error) {
	endpoint, err := buildCDNConfigEndpoint(config.baseURL)
	if err != nil {
		return nil, err
	}
	if config.apiKey == "" {
		return nil, fmt.Errorf("missing DD_API_KEY for feature flag CDN source")
	}
	if config.httpClient == nil {
		config.httpClient = http.DefaultClient
	}
	if config.pollInterval <= 0 {
		config.pollInterval = defaultFeatureFlagCDNPollInterval
	}
	if config.requestTimeout <= 0 {
		config.requestTimeout = defaultFeatureFlagCDNRequestTimeout
	}
	if config.apply == nil {
		return nil, fmt.Errorf("missing CDN configuration apply callback")
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &cdnPoller{
		endpoint:       endpoint,
		apiKey:         config.apiKey,
		pollInterval:   config.pollInterval,
		requestTimeout: config.requestTimeout,
		httpClient:     config.httpClient,
		apply:          config.apply,
		backoff:        config.backoff,
		ctx:            ctx,
		cancel:         cancel,
	}, nil
}

func buildCDNConfigEndpoint(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid feature flag CDN base URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return "", fmt.Errorf("invalid feature flag CDN base URL scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid feature flag CDN base URL: missing host")
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	parsed.Path = path.Join(basePath, defaultCDNConfigPath)
	return parsed.String(), nil
}

func (p *cdnPoller) start() {
	p.wg.Add(1)
	go p.run()
}

func (p *cdnPoller) run() {
	defer p.wg.Done()
	_ = p.pollOnce(p.ctx)

	if p.tickC != nil {
		for {
			select {
			case <-p.ctx.Done():
				return
			case <-p.tickC:
				_ = p.pollOnce(p.ctx)
			}
		}
	}

	ticker := time.NewTicker(p.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			_ = p.pollOnce(p.ctx)
		}
	}
}

func (p *cdnPoller) stop(ctx context.Context) error {
	p.cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.wg.Wait()
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *cdnPoller) pollOnce(ctx context.Context) error {
	body, etag, notModified, err := p.fetch(ctx)
	if err != nil {
		return err
	}
	if notModified {
		return nil
	}

	config, err := parseAndValidateConfiguration(body)
	if err != nil {
		return err
	}
	p.apply(config)

	p.mu.Lock()
	p.etag = etag
	p.mu.Unlock()
	return nil
}

func (p *cdnPoller) fetch(ctx context.Context) ([]byte, string, bool, error) {
	reqCtx, cancel := context.WithTimeout(ctx, p.requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, p.endpoint, nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set(cdnAPIKeyHeader, p.apiKey)
	p.mu.Lock()
	etag := p.etag
	p.mu.Unlock()
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, "", true, nil
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		return nil, "", false, fmt.Errorf("feature flag CDN request failed with status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCDNResponseBodyBytes+1))
	if err != nil {
		return nil, "", false, err
	}
	if len(body) > maxCDNResponseBodyBytes {
		return nil, "", false, fmt.Errorf("feature flag CDN response exceeds %d bytes", maxCDNResponseBodyBytes)
	}
	return body, resp.Header.Get("ETag"), false, nil
}
