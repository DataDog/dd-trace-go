// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	jsoniter "github.com/json-iterator/go"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

const (
	evpProxyV4Path = "/evp_proxy/v4"
	evpProxyV2Path = "/evp_proxy/v2"

	directEVPHostPrefix = "event-platform-intake."
	apiKeyHeader        = "DD-API-KEY"

	defaultEVPRouteRecoveryCooldown = 30 * time.Second
)

var errNoEVPRoute = errors.New("no compatible EVP route is available")

type evpRouteMode uint8

const (
	evpRouteUnknown evpRouteMode = iota
	evpRouteLocal
	evpRouteDirect
	evpRouteDisabled
)

type evpHTTPStatusError struct {
	statusCode int
}

func (e *evpHTTPStatusError) Error() string {
	return fmt.Sprintf("unexpected status code %d", e.statusCode)
}

type evpClient struct {
	httpClient   *http.Client
	directClient *http.Client
	agentURL     *url.URL
	directURL    *url.URL
	apiKey       string
	jsonConfig   jsoniter.API

	routeMu   sync.Mutex
	routeMode evpRouteMode
	localBase string
	recoverAt time.Time
	now       func() time.Time
	cooldown  time.Duration
}

// newEVPClient returns the historical Agent-only transport.
func newEVPClient() *evpClient {
	c := newEVPClientBase()
	c.routeMode = evpRouteLocal
	c.localBase = evpProxyV2Path
	return c
}

// newAgentlessEVPClient prefers a compatible local Agent route and falls back
// to direct intake when credentials are available.
func newAgentlessEVPClient(settings internalffe.Settings) *evpClient {
	c := newEVPClientBase()
	c.apiKey = settings.APIKey
	if c.apiKey != "" {
		c.directURL = buildDirectEVPURL(settings.Site)
		c.directClient = internal.DefaultHTTPClient(defaultHTTPTimeout, false)
		c.directClient.CheckRedirect = refuseEVPRedirect
	}
	return c
}

func newEVPClientBase() *evpClient {
	agentURL := internal.AgentURLFromEnv()
	var httpClient *http.Client
	if agentURL.Scheme == "unix" {
		httpClient = internal.UDSClient(agentURL.Path, defaultHTTPTimeout)
		agentURL = internal.UnixDataSocketURL(agentURL.Path)
	} else {
		httpClient = internal.DefaultHTTPClient(defaultHTTPTimeout, false)
	}
	return &evpClient{
		httpClient: httpClient,
		agentURL:   agentURL,
		jsonConfig: jsoniter.Config{}.Froze(),
		now:        time.Now,
		cooldown:   defaultEVPRouteRecoveryCooldown,
	}
}

func refuseEVPRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func buildDirectEVPURL(site string) *url.URL {
	if site == "" {
		site = agentlessDefaultSite
	}
	if site != strings.TrimSpace(site) {
		return nil
	}
	for i := 0; i < len(site); i++ {
		if site[i] > 0x7f {
			return nil
		}
	}
	site = strings.ToLower(site)
	if site == "" || containsWhitespace(site) || strings.ContainsAny(site, "/\\?#@:") {
		return nil
	}

	expectedHost := directEVPHostPrefix + site
	u, err := url.Parse("https://" + expectedHost)
	if err != nil ||
		u.Scheme != "https" ||
		u.User != nil ||
		u.Host != expectedHost ||
		u.Hostname() != expectedHost ||
		u.Port() != "" ||
		u.Path != "" ||
		u.RawQuery != "" ||
		u.Fragment != "" {
		return nil
	}
	return u
}

func (c *evpClient) post(endpoint, eventName string, payload any) error {
	if c == nil {
		return errors.New("EVP client is not configured")
	}

	var bytesBuffer bytes.Buffer
	encoder := c.jsonConfig.NewEncoder(&bytesBuffer)
	if err := encoder.Encode(payload); err != nil {
		return fmt.Errorf("failed to encode %s payload: %w", eventName, err)
	}
	return c.postRaw(endpoint, eventName, bytesBuffer.Bytes())
}

// postRaw sends already-encoded JSON bytes through the selected EVP route. Used by the
// flagevaluation flush path, which splits a flush into multiple size-bounded payloads and
// encodes each incrementally (see buildFlagEvalPayloads) rather than handing it to post().
func (c *evpClient) postRaw(endpoint, eventName string, body []byte) error {
	if c == nil {
		return errors.New("EVP client is not configured")
	}

	mode, localBase := c.resolveRoute()
	switch mode {
	case evpRouteLocal:
		err := c.send(c.httpClient, c.agentURL, localBase, endpoint, eventName, body, false)
		if err == nil {
			return nil
		}

		var statusErr *evpHTTPStatusError
		if errors.As(err, &statusErr) {
			if statusErr.statusCode != http.StatusNotFound &&
				statusErr.statusCode != http.StatusMethodNotAllowed {
				return err
			}
			if c.leaveLocalRoute() {
				return c.sendDirect(endpoint, eventName, body)
			}
			return err
		}

		// An ambiguous transport error may have happened after the Agent received
		// the body, so change only future routing and do not replay this batch.
		c.leaveLocalRoute()
		return err
	case evpRouteDirect:
		return c.sendDirect(endpoint, eventName, body)
	default:
		return errNoEVPRoute
	}
}

func (c *evpClient) send(
	client *http.Client,
	baseURL *url.URL,
	basePath string,
	endpoint string,
	eventName string,
	body []byte,
	direct bool,
) error {
	if client == nil || baseURL == nil {
		return errNoEVPRoute
	}

	u := *baseURL
	u.Path = joinEVPPath(basePath, endpoint)
	requestURL := u.String()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if direct {
		req.Header.Set(apiKeyHeader, c.apiKey)
	} else {
		req.Header.Set(evpSubdomainHeader, evpSubdomainValue)
	}

	log.Debug("openfeature: sending %s events to %s", eventName, requestURL)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &evpHTTPStatusError{statusCode: resp.StatusCode}
	}
	return nil
}

func (c *evpClient) sendDirect(endpoint, eventName string, body []byte) error {
	return c.send(c.directClient, c.directURL, "", endpoint, eventName, body, true)
}

func (c *evpClient) resolveRoute() (evpRouteMode, string) {
	c.routeMu.Lock()
	defer c.routeMu.Unlock()

	if c.routeMode == evpRouteLocal || c.routeMode == evpRouteDirect {
		return c.routeMode, c.localBase
	}
	now := c.now()
	if c.routeMode == evpRouteDisabled && now.Before(c.recoverAt) {
		return c.routeMode, ""
	}

	if localBase := c.discoverLocalRoute(); localBase != "" {
		c.routeMode = evpRouteLocal
		c.localBase = localBase
		c.recoverAt = time.Time{}
	} else if c.canUseDirect() {
		c.routeMode = evpRouteDirect
		c.localBase = ""
	} else {
		c.routeMode = evpRouteDisabled
		c.localBase = ""
		c.recoverAt = c.now().Add(c.cooldown)
	}
	return c.routeMode, c.localBase
}

func (c *evpClient) discoverLocalRoute() string {
	if c.httpClient == nil || c.agentURL == nil {
		return ""
	}

	u := *c.agentURL
	u.Path = "/info"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u.String(), nil)
	if err != nil {
		return ""
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var info struct {
		Endpoints []string `json:"endpoints"`
	}
	if err := c.jsonConfig.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return ""
	}
	return selectEVPProxyPath(info.Endpoints)
}

func selectEVPProxyPath(endpoints []string) string {
	advertised := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		advertised[strings.TrimRight(endpoint, "/")] = struct{}{}
	}
	for _, supported := range []string{evpProxyV4Path, evpProxyV2Path} {
		if _, ok := advertised[supported]; ok {
			return supported
		}
	}
	return ""
}

func joinEVPPath(basePath, endpoint string) string {
	return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(endpoint, "/")
}

func (c *evpClient) canUseDirect() bool {
	return c.directClient != nil && c.directURL != nil && c.apiKey != ""
}

// leaveLocalRoute selects direct intake for future events when available.
// Without credentials, local discovery becomes eligible again after cooldown.
func (c *evpClient) leaveLocalRoute() bool {
	c.routeMu.Lock()
	defer c.routeMu.Unlock()
	c.localBase = ""
	if c.canUseDirect() {
		c.routeMode = evpRouteDirect
		return true
	}
	c.routeMode = evpRouteDisabled
	c.recoverAt = c.now().Add(c.cooldown)
	return false
}

// marshalJSON encodes a value with the EVP client's jsoniter config. Used by the flagevaluation
// flush path to encode individual events for size-bounded payload splitting.
func (c *evpClient) marshalJSON(v any) ([]byte, error) {
	if c == nil {
		return nil, errors.New("EVP client is not configured")
	}
	return c.jsonConfig.Marshal(v)
}
