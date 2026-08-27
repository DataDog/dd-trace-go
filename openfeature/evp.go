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
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	jsoniter "github.com/json-iterator/go"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

const (
	evpProxyV4Path = "/evp_proxy/v4"
	evpProxyV2Path = "/evp_proxy/v2"

	directEVPHostPrefix = "event-platform-intake."

	apiKeyHeader = "DD-API-KEY"
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
	body       string
}

func (e *evpHTTPStatusError) Error() string {
	return fmt.Sprintf("unexpected status code %d: %s", e.statusCode, e.body)
}

type evpClient struct {
	httpClient   *http.Client
	directClient *http.Client
	agentURL     *url.URL
	directURL    *url.URL
	apiKey       string
	jsonConfig   jsoniter.API

	discoveryOnce sync.Once
	warnNoRoute   sync.Once
	routeMu       sync.RWMutex
	routeMode     evpRouteMode
	localBase     string
}

// newEVPClient returns the historical Agent-only transport. Remote Configuration
// must never fall back to direct intake.
func newEVPClient() *evpClient {
	c := newEVPClientBase()
	c.routeMode = evpRouteLocal
	c.localBase = evpProxyV2Path
	return c
}

// newAgentlessEVPClient builds a source-aware client. Discovery is intentionally
// lazy so provider readiness never waits for the Agent-compatible relay.
func newAgentlessEVPClient(settings internalffe.Settings) *evpClient {
	c := newEVPClientBase()
	c.directClient = internal.DefaultHTTPClient(defaultHTTPTimeout, false)
	// Direct EVP intake has no legitimate reason to redirect. Refusing redirects
	// prevents DD-API-KEY from being forwarded to a redirect target: Go strips
	// only built-in sensitive headers on cross-origin redirects, not this custom
	// credential header.
	c.directClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	c.apiKey = settings.APIKey
	if c.apiKey != "" {
		c.directURL = buildDirectEVPURL(settings.Site)
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
	}
}

func buildDirectEVPURL(site string) *url.URL {
	site = strings.ToLower(strings.TrimSpace(site))
	if !isValidDirectEVPSite(site) {
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
		u.RawPath != "" ||
		u.RawQuery != "" ||
		u.Fragment != "" {
		return nil
	}
	return u
}

func isValidDirectEVPSite(site string) bool {
	if site == "" || len(site) > 230 {
		return false
	}

	labelLength := 0
	previousWasHyphen := false
	for i := 0; i < len(site); i++ {
		character := site[i]
		if character == '.' {
			if labelLength == 0 || previousWasHyphen {
				return false
			}
			labelLength = 0
			previousWasHyphen = false
			continue
		}

		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			labelLength++
			previousWasHyphen = false
		} else if character == '-' && labelLength > 0 {
			labelLength++
			previousWasHyphen = true
		} else {
			return false
		}
		if labelLength > 63 {
			return false
		}
	}

	return labelLength > 0 && !previousWasHyphen
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
// flagevaluation flush path, which splits a flush into multiple size-bounded payloads.
func (c *evpClient) postRaw(endpoint, eventName string, body []byte) error {
	if c == nil {
		return errors.New("EVP client is not configured")
	}

	mode, localBase := c.resolveRoute()
	switch mode {
	case evpRouteLocal:
		result := c.send(c.httpClient, c.agentURL, localBase, endpoint, eventName, body, false)
		if result.err == nil {
			return nil
		}

		if !c.canUseDirect() {
			return result.err
		}

		var statusErr *evpHTTPStatusError
		if errors.As(result.err, &statusErr) {
			if statusErr.statusCode != http.StatusForbidden &&
				statusErr.statusCode != http.StatusNotFound &&
				statusErr.statusCode != http.StatusMethodNotAllowed {
				return result.err
			}
			c.selectDirect()
			return c.sendDirect(endpoint, eventName, body)
		}

		// Every transport error changes only future routing. Replaying the current
		// body is allowed solely when the request was never written and the error
		// proves DNS/socket establishment failed.
		c.selectDirect()
		if !result.wroteRequest && isDefinitivePreSendError(result.err) {
			return c.sendDirect(endpoint, eventName, body)
		}
		return result.err
	case evpRouteDirect:
		return c.sendDirect(endpoint, eventName, body)
	default:
		c.warnNoRoute.Do(func() {
			log.Warn("openfeature: disabling EVP event delivery because no compatible local route or direct credentials are available")
		})
		return errNoEVPRoute
	}
}

type evpSendResult struct {
	err          error
	wroteRequest bool
}

func (c *evpClient) send(
	client *http.Client,
	baseURL *url.URL,
	basePath string,
	endpoint string,
	eventName string,
	body []byte,
	direct bool,
) evpSendResult {
	if client == nil || baseURL == nil {
		return evpSendResult{err: errNoEVPRoute}
	}

	u := *baseURL
	u.Path = joinEVPPath(basePath, endpoint)
	requestURL := u.String()

	var wroteRequest atomic.Bool
	trace := &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) {
			wroteRequest.Store(true)
		},
	}
	ctx := httptrace.WithClientTrace(context.Background(), trace)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return evpSendResult{err: fmt.Errorf("failed to create request: %w", err)}
	}

	req.Header.Set("Content-Type", "application/json")
	if direct {
		req.Header.Set(apiKeyHeader, c.apiKey)
	} else {
		req.Header.Set(evpSubdomainHeader, evpSubdomainValue)
	}

	log.Debug("openfeature: sending %s events through %s EVP route", eventName, routeName(direct))

	resp, err := client.Do(req)
	if err != nil {
		return evpSendResult{
			err:          fmt.Errorf("request failed: %w", err),
			wroteRequest: wroteRequest.Load(),
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return evpSendResult{
			err: &evpHTTPStatusError{
				statusCode: resp.StatusCode,
				body:       string(respBody),
			},
			wroteRequest: wroteRequest.Load(),
		}
	}
	return evpSendResult{wroteRequest: wroteRequest.Load()}
}

func routeName(direct bool) string {
	if direct {
		return "direct"
	}
	return "local"
}

func (c *evpClient) sendDirect(endpoint, eventName string, body []byte) error {
	return c.send(c.directClient, c.directURL, "", endpoint, eventName, body, true).err
}

func (c *evpClient) resolveRoute() (evpRouteMode, string) {
	c.routeMu.RLock()
	mode, localBase := c.routeMode, c.localBase
	c.routeMu.RUnlock()
	if mode != evpRouteUnknown {
		return mode, localBase
	}

	c.discoveryOnce.Do(c.discoverLocalRoute)
	c.routeMu.RLock()
	defer c.routeMu.RUnlock()
	return c.routeMode, c.localBase
}

func (c *evpClient) discoverLocalRoute() {
	selected := ""
	if c.httpClient != nil && c.agentURL != nil {
		u := *c.agentURL
		u.Path = joinEVPPath("", "/info")
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, u.String(), nil)
		if err == nil {
			resp, doErr := c.httpClient.Do(req)
			if doErr == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					var info struct {
						Endpoints []string `json:"endpoints"`
					}
					if decodeErr := c.jsonConfig.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); decodeErr == nil {
						selected = selectEVPProxyPath(info.Endpoints)
					}
				}
			}
		}
	}

	c.routeMu.Lock()
	defer c.routeMu.Unlock()
	if selected != "" {
		c.routeMode = evpRouteLocal
		c.localBase = selected
	} else if c.canUseDirectLocked() {
		c.routeMode = evpRouteDirect
	} else {
		c.routeMode = evpRouteDisabled
	}
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
	basePath = strings.TrimRight(basePath, "/")
	endpoint = "/" + strings.TrimLeft(endpoint, "/")
	return basePath + endpoint
}

func (c *evpClient) canUseDirect() bool {
	c.routeMu.RLock()
	defer c.routeMu.RUnlock()
	return c.canUseDirectLocked()
}

func (c *evpClient) canUseDirectLocked() bool {
	return c.directClient != nil && c.directURL != nil && c.apiKey != ""
}

func (c *evpClient) selectDirect() {
	c.routeMu.Lock()
	defer c.routeMu.Unlock()
	if c.canUseDirectLocked() {
		c.routeMode = evpRouteDirect
		c.localBase = ""
	}
}

func isDefinitivePreSendError(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT) {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && (dnsErr.IsNotFound || dnsErr.IsTemporary)
}

// marshalJSON encodes a value with the EVP client's jsoniter config. Used by the flagevaluation
// flush path to encode individual events for size-bounded payload splitting.
func (c *evpClient) marshalJSON(v any) ([]byte, error) {
	if c == nil {
		return nil, errors.New("EVP client is not configured")
	}
	return c.jsonConfig.Marshal(v)
}
