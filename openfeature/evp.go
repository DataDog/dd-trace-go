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
	"time"

	jsoniter "github.com/json-iterator/go"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

const (
	headerEVPOrigin        = "DD-EVP-ORIGIN"
	evpOrigin              = "dd-trace-go"
	headerEVPOriginVersion = "DD-EVP-ORIGIN-VERSION"
)

const (
	evpProxyV4Path = "/evp_proxy/v4"
	evpProxyV2Path = "/evp_proxy/v2"

	directEVPHostPrefix = "event-platform-intake."

	apiKeyHeader = "DD-API-KEY"

	// defaultEVPRouteRecoveryCooldown bounds how long direct or unavailable
	// routing remains sticky before agentless delivery probes the preferred
	// local relay again. Tests replace the clock rather than sleeping.
	defaultEVPRouteRecoveryCooldown = 30 * time.Second
)

var (
	errNoEVPRoute     = errors.New("no compatible EVP route is available")
	errInvalidEVPPath = errors.New("unsupported EVP event path")
)

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

	warnNoRoute sync.Once

	// discoveryToken serializes the potentially blocking /info request without
	// holding routeMu. Waiting callers can still honor cancellation and re-check
	// the route after taking it, so only one probe occurs per recovery window.
	discoveryToken chan struct{}
	routeMu        sync.RWMutex
	routeMode      evpRouteMode
	localBase      string
	recoverAt      time.Time
	now            func() time.Time
	cooldown       time.Duration
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
	c.directClient.CheckRedirect = refuseEVPRedirect
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
	httpClient.CheckRedirect = refuseEVPRedirect

	c := &evpClient{
		httpClient:     httpClient,
		agentURL:       agentURL,
		jsonConfig:     jsoniter.Config{}.Froze(),
		discoveryToken: make(chan struct{}, 1),
		now:            time.Now,
		cooldown:       defaultEVPRouteRecoveryCooldown,
	}
	c.discoveryToken <- struct{}{}
	return c
}

func refuseEVPRedirect(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func buildDirectEVPURL(site string) *url.URL {
	site, ok := normalizeAgentlessSite(site)
	if !ok {
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

func (c *evpClient) post(endpoint, eventName string, payload any) error {
	return c.postWithContext(context.Background(), endpoint, eventName, payload)
}

func (c *evpClient) postWithContext(ctx context.Context, endpoint, eventName string, payload any) error {
	if c == nil {
		return errors.New("EVP client is not configured")
	}

	var bytesBuffer bytes.Buffer
	encoder := c.jsonConfig.NewEncoder(&bytesBuffer)
	if err := encoder.Encode(payload); err != nil {
		return fmt.Errorf("failed to encode %s payload: %w", eventName, err)
	}
	return c.postRawWithContext(ctx, endpoint, eventName, bytesBuffer.Bytes())
}

// postRaw sends already-encoded JSON bytes through the selected EVP route. Used by the
// flagevaluation flush path, which splits a flush into multiple size-bounded payloads.
func (c *evpClient) postRaw(endpoint, eventName string, body []byte) error {
	return c.postRawWithContext(context.Background(), endpoint, eventName, body)
}

func (c *evpClient) postRawWithContext(ctx context.Context, endpoint, eventName string, body []byte) error {
	if c == nil {
		return errors.New("EVP client is not configured")
	}
	if endpoint != exposureEndpoint && endpoint != flagEvalLoggingEndpoint {
		return fmt.Errorf("%w: %q", errInvalidEVPPath, endpoint)
	}

	mode, localBase, routeErr := c.resolveRoute(ctx)
	if routeErr != nil {
		return routeErr
	}
	switch mode {
	case evpRouteLocal:
		result := c.send(ctx, c.httpClient, c.agentURL, localBase, endpoint, eventName, body, false)
		if result.err == nil {
			return nil
		}

		if ctx.Err() != nil {
			// Caller cancellation does not prove the shared local route is bad.
			return result.err
		}

		var statusErr *evpHTTPStatusError
		if errors.As(result.err, &statusErr) {
			if statusErr.statusCode != http.StatusNotFound &&
				statusErr.statusCode != http.StatusMethodNotAllowed {
				return result.err
			}
			// A 404/405 authoritatively invalidates the advertised local path.
			// Preserve that information even without direct credentials so a
			// later cooldown probe can discover an Agent v2/v4 route change.
			if !c.invalidateLocalRoute() {
				return result.err
			}
			return c.sendDirect(ctx, endpoint, eventName, body)
		}
		if !c.canUseDirect() {
			return result.err
		}

		// Every transport error changes only future routing. Replaying the current
		// body is allowed solely when the request was never written and the error
		// proves DNS/socket establishment failed.
		c.selectDirect()
		if !result.wroteRequest && isDefinitivePreSendError(result.err) {
			return c.sendDirect(ctx, endpoint, eventName, body)
		}
		return result.err
	case evpRouteDirect:
		return c.sendDirect(ctx, endpoint, eventName, body)
	default:
		c.warnNoRoute.Do(func() {
			log.Warn("openfeature: EVP event delivery has no compatible local route or direct credentials; local discovery will retry after a cooldown")
		})
		return errNoEVPRoute
	}
}

type evpSendResult struct {
	err          error
	wroteRequest bool
}

func (c *evpClient) send(
	ctx context.Context,
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
	requestCtx := httptrace.WithClientTrace(ctx, trace)
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return evpSendResult{err: fmt.Errorf("failed to create request: %w", err)}
	}

	req.Header.Set("Content-Type", "application/json")
	if direct {
		req.Header.Set(apiKeyHeader, c.apiKey)
	} else {
		req.Header.Set(evpSubdomainHeader, evpSubdomainValue)
	}
	req.Header.Set(headerEVPOrigin, evpOrigin)
	req.Header.Set(headerEVPOriginVersion, version.Tag)

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
		return evpSendResult{
			err: &evpHTTPStatusError{
				statusCode: resp.StatusCode,
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

func (c *evpClient) sendDirect(ctx context.Context, endpoint, eventName string, body []byte) error {
	return c.send(ctx, c.directClient, c.directURL, "", endpoint, eventName, body, true).err
}

func (c *evpClient) resolveRoute(ctx context.Context) (evpRouteMode, string, error) {
	c.routeMu.RLock()
	mode, localBase, recoverAt := c.routeMode, c.localBase, c.recoverAt
	c.routeMu.RUnlock()
	if mode == evpRouteLocal {
		return mode, localBase, nil
	}
	now := c.now()
	if !routeNeedsDiscovery(mode, now, recoverAt) {
		return mode, localBase, nil
	}

	select {
	case <-ctx.Done():
		return mode, localBase, ctx.Err()
	case <-c.discoveryToken:
	}
	defer func() { c.discoveryToken <- struct{}{} }()

	// Another caller may have completed discovery while this one waited.
	now = c.now()
	c.routeMu.RLock()
	mode, localBase, recoverAt = c.routeMode, c.localBase, c.recoverAt
	c.routeMu.RUnlock()
	if !routeNeedsDiscovery(mode, now, recoverAt) {
		return mode, localBase, nil
	}

	selected, discoveryErr := c.discoverLocalRoute(ctx)
	if discoveryErr != nil {
		// A caller's cancellation says nothing about global route health. Leave
		// the expired state untouched so the next live caller can probe now.
		return mode, localBase, discoveryErr
	}
	if err := ctx.Err(); err != nil {
		return mode, localBase, err
	}
	now = c.now()
	c.routeMu.Lock()
	defer c.routeMu.Unlock()
	if selected != "" {
		c.routeMode = evpRouteLocal
		c.localBase = selected
		c.recoverAt = time.Time{}
	} else {
		c.localBase = ""
		if c.canUseDirectLocked() {
			c.routeMode = evpRouteDirect
		} else {
			c.routeMode = evpRouteDisabled
		}
		c.recoverAt = now.Add(c.cooldown)
	}
	return c.routeMode, c.localBase, nil
}

func routeNeedsDiscovery(mode evpRouteMode, now, recoverAt time.Time) bool {
	return mode == evpRouteUnknown ||
		((mode == evpRouteDirect || mode == evpRouteDisabled) && !now.Before(recoverAt))
}

func (c *evpClient) discoverLocalRoute(ctx context.Context) (string, error) {
	if c.httpClient != nil && c.agentURL != nil {
		u := *c.agentURL
		u.Path = joinEVPPath("", "/info")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err == nil {
			resp, doErr := c.httpClient.Do(req)
			if doErr == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					var info struct {
						Endpoints []string `json:"endpoints"`
					}
					if decodeErr := c.jsonConfig.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); decodeErr == nil {
						selected := selectEVPProxyPath(info.Endpoints)
						if ctx.Err() != nil {
							return "", ctx.Err()
						}
						return selected, nil
					}
				}
			}
		}
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", nil
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
		c.recoverAt = c.now().Add(c.cooldown)
	}
}

// invalidateLocalRoute records that an authoritative response rejected the
// selected Agent path. It returns whether direct delivery is available for a
// safe same-batch replay; otherwise the client remains recoverable after the
// cooldown without replaying the current batch.
func (c *evpClient) invalidateLocalRoute() bool {
	c.routeMu.Lock()
	defer c.routeMu.Unlock()
	c.localBase = ""
	canUseDirect := c.canUseDirectLocked()
	if canUseDirect {
		c.routeMode = evpRouteDirect
	} else {
		c.routeMode = evpRouteDisabled
	}
	c.recoverAt = c.now().Add(c.cooldown)
	return canUseDirect
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
