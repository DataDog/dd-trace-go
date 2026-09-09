// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	internallog "github.com/DataDog/dd-trace-go/v2/internal/log"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

const (
	testAPIKey = "system-tests-mock-api-key"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(&emptyReader{}),
	}
}

type emptyReader struct{}

func (*emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

type fakeEVPClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (c *fakeEVPClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *fakeEVPClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func useFakeEVPClock(c *evpClient) *fakeEVPClock {
	clock := &fakeEVPClock{now: time.Unix(1_700_000_000, 0)}
	c.now = clock.Now
	c.cooldown = time.Minute
	return clock
}

func assertEVPIdentity(t *testing.T, req *http.Request) {
	t.Helper()
	if req.Method != http.MethodPost {
		t.Errorf("method = %q, want %q", req.Method, http.MethodPost)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
	if got := req.Header.Get(headerEVPOrigin); got != evpOrigin {
		t.Errorf("%s = %q, want %q", headerEVPOrigin, got, evpOrigin)
	}
	if got := req.Header.Get(headerEVPOriginVersion); got != version.Tag {
		t.Errorf("%s = %q, want %q", headerEVPOriginVersion, got, version.Tag)
	}
}

func configuredAgentlessEVP(local, direct http.RoundTripper) *evpClient {
	c := newAgentlessEVPClient(internalffe.Settings{
		Source: internalffe.SourceAgentless,
		Site:   "mock-intake.invalid",
		APIKey: testAPIKey,
	})
	c.httpClient = &http.Client{Transport: local}
	c.directClient = &http.Client{Transport: direct}
	c.agentURL = &url.URL{Scheme: "http", Host: "local.invalid"}
	c.directURL = &url.URL{Scheme: "https", Host: "event-platform-intake.mock-intake.invalid"}
	c.routeMode = evpRouteLocal
	c.localBase = evpProxyV2Path
	return c
}

func TestBuildDirectEVPURL(t *testing.T) {
	for _, tc := range []struct {
		name     string
		site     string
		wantHost string
	}{
		{name: "unset site", site: "", wantHost: "event-platform-intake.datadoghq.com"},
		{name: "default site", site: "datadoghq.com", wantHost: "event-platform-intake.datadoghq.com"},
		{name: "custom domain", site: "custom.example", wantHost: "event-platform-intake.custom.example"},
		{name: "uppercase domain", site: "DATADOGHQ.EU", wantHost: "event-platform-intake.datadoghq.eu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := buildDirectEVPURL(tc.site)
			if u == nil {
				t.Fatal("buildDirectEVPURL() returned nil")
			}
			if u.Scheme != "https" || u.User != nil || u.Host != tc.wantHost || u.Hostname() != tc.wantHost || u.Port() != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
				t.Fatalf("buildDirectEVPURL() = %#v, want exact HTTPS host %q", u, tc.wantHost)
			}
		})
	}
}

func TestBuildDirectEVPURLRejectsUnsafeSite(t *testing.T) {
	for _, tc := range []struct {
		name string
		site string
	}{
		{name: "userinfo", site: "datadoghq.com@evil.example"},
		{name: "userinfo with password", site: "datadoghq.com:password@evil.example"},
		{name: "scheme", site: "https://datadoghq.com"},
		{name: "default port", site: "datadoghq.com:443"},
		{name: "custom port", site: "datadoghq.com:8443"},
		{name: "path", site: "datadoghq.com/path"},
		{name: "query", site: "datadoghq.com?query=value"},
		{name: "fragment", site: "datadoghq.com#fragment"},
		{name: "leading whitespace", site: " datadoghq.com"},
		{name: "trailing whitespace", site: "datadoghq.com\t"},
		{name: "internal whitespace", site: "data doghq.com"},
		{name: "backslash", site: "datadoghq.com\\evil.example"},
		{name: "percent-encoded dot", site: "datadoghq.com%2eattacker.example"},
		{name: "ideographic full stop", site: "datadoghq.com。attacker.example"},
		{name: "fullwidth full stop", site: "datadoghq.com．attacker.example"},
		{name: "halfwidth ideographic full stop", site: "datadoghq.com｡attacker.example"},
		{name: "unicode character lowercasing to ASCII", site: "K.com"},
		{name: "unicode dotted I lowercasing to ASCII", site: "İ.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildDirectEVPURL(tc.site); got != nil {
				t.Fatalf("buildDirectEVPURL(%q) = %s, want nil", tc.site, got)
			}
		})
	}
}

func TestAgentlessEVPDirectConstructorPreservesExactHostAndCredentials(t *testing.T) {
	client := newAgentlessEVPClient(internalffe.Settings{
		Source: internalffe.SourceAgentless,
		Site:   "CUSTOM.EXAMPLE",
		APIKey: testAPIKey,
	})
	client.agentURL = &url.URL{Scheme: "http", Host: "agent.invalid"}
	client.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"endpoints":[]}`)),
			Header:     make(http.Header),
		}, nil
	})}
	client.directClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Scheme != "https" || req.URL.Host != "event-platform-intake.custom.example" {
			t.Fatalf("direct request URL = %s, want exact configured HTTPS host", req.URL)
		}
		if got := req.Header.Get(apiKeyHeader); got != testAPIKey {
			t.Fatalf("direct API key = %q, want configured key", got)
		}
		if got := req.Header.Get(evpSubdomainHeader); got != "" {
			t.Fatalf("direct request carried local EVP header %q", got)
		}
		assertEVPIdentity(t, req)
		return newResponse(http.StatusAccepted), nil
	})}

	if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func TestDirectEligibilityRequiresAgentlessProviderSource(t *testing.T) {
	provider := newDatadogProviderWithSource(
		ProviderConfig{},
		internalffe.SourceRemoteConfig,
		internalffe.Settings{
			Source: internalffe.SourceAgentless,
			Site:   "mock-intake.invalid",
			APIKey: testAPIKey,
		},
	)
	client := provider.exposureWriter.evp
	if client.directClient != nil || client.directURL != nil || client.apiKey != "" {
		t.Fatal("remote_config provider unexpectedly enabled direct EVP credentials")
	}
	if client.routeMode != evpRouteLocal || client.localBase != evpProxyV2Path {
		t.Fatalf("remote_config route = (%v, %q), want historical local v2", client.routeMode, client.localBase)
	}
}

func TestAgentlessEVPUnsafeSiteNeverReachesDirectTransport(t *testing.T) {
	for _, site := range []string{
		" datadoghq.com",
		"datadoghq.com\t",
		"datadoghq.com%2eattacker.example",
		"datadoghq.com。attacker.example",
		"datadoghq.com．attacker.example",
		"datadoghq.com｡attacker.example",
		"K.com",
		"İ.com",
	} {
		t.Run(site, func(t *testing.T) {
			var directRequests atomic.Int64
			client := newAgentlessEVPClient(internalffe.Settings{
				Source: internalffe.SourceAgentless,
				Site:   site,
				APIKey: testAPIKey,
			})
			client.agentURL = &url.URL{Scheme: "http", Host: "agent.invalid"}
			client.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"endpoints":[]}`)),
					Header:     make(http.Header),
				}, nil
			})}
			client.directClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				directRequests.Add(1)
				return newResponse(http.StatusAccepted), nil
			})}

			err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`))
			if !errors.Is(err, errNoEVPRoute) {
				t.Fatalf("postRaw() error = %v, want %v", err, errNoEVPRoute)
			}
			if directRequests.Load() != 0 {
				t.Fatalf("direct transport received %d request(s), want 0", directRequests.Load())
			}
		})
	}
}

func TestSelectEVPProxyPath(t *testing.T) {
	tests := []struct {
		name      string
		endpoints []string
		want      string
	}{
		{name: "v4 preferred", endpoints: []string{"/evp_proxy/v2/", "/evp_proxy/v4/"}, want: evpProxyV4Path},
		{name: "v2 fallback", endpoints: []string{"/v0.4/traces", "/evp_proxy/v2/"}, want: evpProxyV2Path},
		{name: "unsupported", endpoints: []string{"/v0.4/traces"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectEVPProxyPath(tt.endpoints); got != tt.want {
				t.Fatalf("selectEVPProxyPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentlessEVPDiscoveryAndLocalHeaders(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoints string
		wantBase  string
	}{
		{name: "v4", endpoints: `{"endpoints":["/evp_proxy/v2/","/evp_proxy/v4/"]}`, wantBase: evpProxyV4Path},
		{name: "v2", endpoints: `{"endpoints":["/evp_proxy/v2/"]}`, wantBase: evpProxyV2Path},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var infoRequests atomic.Int64
			var eventRequests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/info":
					infoRequests.Add(1)
					_, _ = io.WriteString(w, tc.endpoints)
				case joinEVPPath(tc.wantBase, exposureEndpoint):
					eventRequests.Add(1)
					if got := r.Header.Get(apiKeyHeader); got != "" {
						t.Errorf("local request carried API key %q", got)
					}
					if got := r.Header.Get(evpSubdomainHeader); got != evpSubdomainValue {
						t.Errorf("local EVP header = %q, want %q", got, evpSubdomainValue)
					}
					assertEVPIdentity(t, r)
					w.WriteHeader(http.StatusAccepted)
				default:
					t.Errorf("unexpected request path %q", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			agentURL, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client := newAgentlessEVPClient(internalffe.Settings{Source: internalffe.SourceAgentless})
			client.agentURL = agentURL
			client.httpClient = server.Client()

			if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err != nil {
				t.Fatal(err)
			}
			if infoRequests.Load() != 1 || eventRequests.Load() != 1 {
				t.Fatalf("requests: info=%d event=%d, want 1 each", infoRequests.Load(), eventRequests.Load())
			}
		})
	}
}

func TestAgentlessEVPDirectCredentialsAndBothSignals(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" {
			t.Errorf("unexpected local path %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"endpoints":[]}`)
	}))
	defer local.Close()

	var mu sync.Mutex
	var paths []string
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if got := r.Header.Get(apiKeyHeader); got != testAPIKey {
			t.Errorf("direct API key = %q, want configured key", got)
		}
		if got := r.Header.Get(evpSubdomainHeader); got != "" {
			t.Errorf("direct request carried local EVP header %q", got)
		}
		assertEVPIdentity(t, r)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer direct.Close()

	localURL, _ := url.Parse(local.URL)
	directURL, _ := url.Parse(direct.URL)
	client := newAgentlessEVPClient(internalffe.Settings{
		Source: internalffe.SourceAgentless,
		Site:   "mock-intake.invalid",
		APIKey: testAPIKey,
	})
	client.agentURL = localURL
	client.httpClient = local.Client()
	client.directURL = directURL
	client.directClient = direct.Client()

	for _, endpoint := range []string{exposureEndpoint, flagEvalLoggingEndpoint} {
		if err := client.postRaw(endpoint, "test", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != exposureEndpoint || paths[1] != flagEvalLoggingEndpoint {
		t.Fatalf("direct paths = %v", paths)
	}
}

func TestRemoteConfigEVPRemainsAgentOnly(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != joinEVPPath(evpProxyV2Path, exposureEndpoint) {
			t.Errorf("unexpected Agent path %q", r.URL.Path)
		}
		if r.Header.Get(apiKeyHeader) != "" {
			t.Error("Remote Configuration Agent request carried direct credentials")
		}
		if got := r.Header.Get(evpSubdomainHeader); got != evpSubdomainValue {
			t.Errorf("Remote Configuration Agent request local EVP header = %q, want %q", got, evpSubdomainValue)
		}
		assertEVPIdentity(t, r)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	agentURL, _ := url.Parse(server.URL)
	client := newEVPClient()
	client.agentURL = agentURL
	client.httpClient = server.Client()
	if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("Agent requests = %d, want 1", requests.Load())
	}
}

func TestAgentlessEVPFallbackFailureMatrix(t *testing.T) {
	t.Run("definitive connect failure replays current batch direct", func(t *testing.T) {
		var localRequests, directRequests atomic.Int64
		client := configuredAgentlessEVP(
			roundTripFunc(func(*http.Request) (*http.Response, error) {
				localRequests.Add(1)
				return nil, syscall.ECONNREFUSED
			}),
			roundTripFunc(func(*http.Request) (*http.Response, error) {
				directRequests.Add(1)
				return newResponse(http.StatusAccepted), nil
			}),
		)
		if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		if localRequests.Load() != 1 || directRequests.Load() != 1 {
			t.Fatalf("requests: local=%d direct=%d, want 1 each", localRequests.Load(), directRequests.Load())
		}
	})

	t.Run("404 and 405 replay current batch direct", func(t *testing.T) {
		for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
			t.Run(http.StatusText(status), func(t *testing.T) {
				var localRequests, directRequests atomic.Int64
				client := configuredAgentlessEVP(
					roundTripFunc(func(*http.Request) (*http.Response, error) {
						localRequests.Add(1)
						return newResponse(status), nil
					}),
					roundTripFunc(func(*http.Request) (*http.Response, error) {
						directRequests.Add(1)
						return newResponse(http.StatusAccepted), nil
					}),
				)
				if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err != nil {
					t.Fatal(err)
				}
				if localRequests.Load() != 1 || directRequests.Load() != 1 {
					t.Fatalf("requests: local=%d direct=%d, want 1 each", localRequests.Load(), directRequests.Load())
				}
			})
		}
	})

	t.Run("ambiguous failures never replay current batch", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			err  error
		}{
			{name: "connection reset", err: syscall.ECONNRESET},
			{name: "broken pipe", err: syscall.EPIPE},
			{name: "timeout", err: context.DeadlineExceeded},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var localRequests, directRequests atomic.Int64
				client := configuredAgentlessEVP(
					roundTripFunc(func(req *http.Request) (*http.Response, error) {
						localRequests.Add(1)
						if trace := httptrace.ContextClientTrace(req.Context()); trace != nil && trace.WroteRequest != nil {
							trace.WroteRequest(httptrace.WroteRequestInfo{})
						}
						return nil, tc.err
					}),
					roundTripFunc(func(*http.Request) (*http.Response, error) {
						directRequests.Add(1)
						return newResponse(http.StatusAccepted), nil
					}),
				)
				if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{"batch":1}`)); err == nil {
					t.Fatal("ambiguous local failure unexpectedly succeeded")
				}
				if directRequests.Load() != 0 {
					t.Fatalf("current ambiguous batch was replayed direct %d times", directRequests.Load())
				}
				if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{"batch":2}`)); err != nil {
					t.Fatal(err)
				}
				if localRequests.Load() != 1 || directRequests.Load() != 1 {
					t.Fatalf("requests after next batch: local=%d direct=%d, want 1 each", localRequests.Load(), directRequests.Load())
				}
			})
		}
	})

	t.Run("403 429 and 5xx remain local", func(t *testing.T) {
		for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
			t.Run(http.StatusText(status), func(t *testing.T) {
				var localRequests, directRequests atomic.Int64
				client := configuredAgentlessEVP(
					roundTripFunc(func(*http.Request) (*http.Response, error) {
						localRequests.Add(1)
						return newResponse(status), nil
					}),
					roundTripFunc(func(*http.Request) (*http.Response, error) {
						directRequests.Add(1)
						return newResponse(http.StatusAccepted), nil
					}),
				)
				for range 2 {
					if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err == nil {
						t.Fatalf("status %d unexpectedly succeeded", status)
					}
				}
				if localRequests.Load() != 2 || directRequests.Load() != 0 {
					t.Fatalf("requests: local=%d direct=%d, want local=2 direct=0", localRequests.Load(), directRequests.Load())
				}
			})
		}
	})
}

func TestAgentlessEVPCanceledLocalSendDoesNotSwitchRoute(t *testing.T) {
	var localRequests, directRequests atomic.Int64
	requestStarted := make(chan struct{})
	client := configuredAgentlessEVP(
		roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if localRequests.Add(1) == 1 {
				close(requestStarted)
				<-req.Context().Done()
				return nil, req.Context().Err()
			}
			return newResponse(http.StatusAccepted), nil
		}),
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			directRequests.Add(1)
			return newResponse(http.StatusAccepted), nil
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- client.postRawWithContext(ctx, exposureEndpoint, "exposure", []byte(`{}`))
	}()
	<-requestStarted
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled local send error = %v, want %v", err, context.Canceled)
	}

	client.routeMu.RLock()
	mode, localBase := client.routeMode, client.localBase
	client.routeMu.RUnlock()
	if mode != evpRouteLocal || localBase != evpProxyV2Path {
		t.Fatalf("route after caller cancellation = (%v, %q), want unchanged local v2", mode, localBase)
	}
	if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err != nil {
		t.Fatalf("next live local send: %v", err)
	}
	if localRequests.Load() != 2 || directRequests.Load() != 0 {
		t.Fatalf("requests: local=%d direct=%d, want 2 and 0", localRequests.Load(), directRequests.Load())
	}
}

func TestAgentlessEVPUnsafeDirectSiteRecoversLocalAfterCooldown(t *testing.T) {
	var infoRequests, eventRequests atomic.Int64
	var recovered atomic.Bool
	client := newAgentlessEVPClient(internalffe.Settings{
		Source: internalffe.SourceAgentless,
		Site:   "K.com",
		APIKey: testAPIKey,
	})
	if client.directURL != nil {
		t.Fatalf("unsafe site produced direct URL %s", client.directURL)
	}
	clock := useFakeEVPClock(client)
	client.agentURL = &url.URL{Scheme: "http", Host: "local.invalid"}
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/info":
			infoRequests.Add(1)
			endpoints := `{"endpoints":[]}`
			if recovered.Load() {
				endpoints = `{"endpoints":["/evp_proxy/v2/"]}`
			}
			resp := newResponse(http.StatusOK)
			resp.Body = io.NopCloser(strings.NewReader(endpoints))
			return resp, nil
		case joinEVPPath(evpProxyV2Path, exposureEndpoint):
			eventRequests.Add(1)
			if got := req.Header.Get(apiKeyHeader); got != "" {
				t.Errorf("recovered local request carried API key %q", got)
			}
			assertEVPIdentity(t, req)
			return newResponse(http.StatusAccepted), nil
		default:
			t.Fatalf("unexpected path %q", req.URL.Path)
			return nil, errors.New("unexpected path")
		}
	})}

	if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); !errors.Is(err, errNoEVPRoute) {
		t.Fatalf("initial postRaw() error = %v, want %v", err, errNoEVPRoute)
	}
	recovered.Store(true)
	clock.Advance(time.Minute)
	if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err != nil {
		t.Fatalf("second postRaw() after recovery: %v", err)
	}
	if infoRequests.Load() != 2 || eventRequests.Load() != 1 {
		t.Fatalf("requests after recovery: info=%d event=%d, want 2 and 1", infoRequests.Load(), eventRequests.Load())
	}
}

func TestAgentlessEVPNoKeyRecoversFromStaleV2ToV4AfterCooldown(t *testing.T) {
	var infoRequests, v2Events, v4Events atomic.Int64
	var advertiseV4 atomic.Bool
	client := newAgentlessEVPClient(internalffe.Settings{Source: internalffe.SourceAgentless})
	clock := useFakeEVPClock(client)
	client.agentURL = &url.URL{Scheme: "http", Host: "local.invalid"}
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/info":
			infoRequests.Add(1)
			endpoint := evpProxyV2Path
			if advertiseV4.Load() {
				endpoint = evpProxyV4Path
			}
			resp := newResponse(http.StatusOK)
			resp.Body = io.NopCloser(strings.NewReader(`{"endpoints":["` + endpoint + `/"]}`))
			return resp, nil
		case joinEVPPath(evpProxyV2Path, exposureEndpoint):
			v2Events.Add(1)
			return newResponse(http.StatusNotFound), nil
		case joinEVPPath(evpProxyV4Path, exposureEndpoint):
			v4Events.Add(1)
			if got := req.Header.Get(apiKeyHeader); got != "" {
				t.Errorf("recovered local request carried API key %q", got)
			}
			assertEVPIdentity(t, req)
			return newResponse(http.StatusAccepted), nil
		default:
			t.Fatalf("unexpected path %q", req.URL.Path)
			return nil, errors.New("unexpected path")
		}
	})}

	err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`))
	var statusErr *evpHTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.statusCode != http.StatusNotFound {
		t.Fatalf("initial postRaw() error = %v, want HTTP status %d", err, http.StatusNotFound)
	}
	client.routeMu.RLock()
	mode, recoverAt := client.routeMode, client.recoverAt
	client.routeMu.RUnlock()
	if mode != evpRouteDisabled || !recoverAt.Equal(clock.Now().Add(time.Minute)) {
		t.Fatalf("route after stale v2 = (%v, %v), want disabled until %v", mode, recoverAt, clock.Now().Add(time.Minute))
	}

	if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); !errors.Is(err, errNoEVPRoute) {
		t.Fatalf("post before cooldown error = %v, want %v", err, errNoEVPRoute)
	}
	if infoRequests.Load() != 1 || v2Events.Load() != 1 || v4Events.Load() != 0 {
		t.Fatalf("requests before cooldown: info=%d v2=%d v4=%d", infoRequests.Load(), v2Events.Load(), v4Events.Load())
	}

	advertiseV4.Store(true)
	clock.Advance(time.Minute)
	if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err != nil {
		t.Fatalf("post after v4 recovery: %v", err)
	}
	if infoRequests.Load() != 2 || v2Events.Load() != 1 || v4Events.Load() != 1 {
		t.Fatalf("requests after recovery: info=%d v2=%d v4=%d, want 2, 1, 1", infoRequests.Load(), v2Events.Load(), v4Events.Load())
	}
}

func TestAgentlessEVPDirectRouteRecoversLocalAfterCooldown(t *testing.T) {
	var infoRequests, localEvents, directEvents atomic.Int64
	var recovered atomic.Bool
	client := newAgentlessEVPClient(internalffe.Settings{
		Source: internalffe.SourceAgentless,
		Site:   "mock-intake.invalid",
		APIKey: testAPIKey,
	})
	clock := useFakeEVPClock(client)
	client.agentURL = &url.URL{Scheme: "http", Host: "local.invalid"}
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/info":
			infoRequests.Add(1)
			endpoints := `{"endpoints":[]}`
			if recovered.Load() {
				endpoints = `{"endpoints":["/evp_proxy/v4/"]}`
			}
			resp := newResponse(http.StatusOK)
			resp.Body = io.NopCloser(strings.NewReader(endpoints))
			return resp, nil
		case joinEVPPath(evpProxyV4Path, exposureEndpoint):
			localEvents.Add(1)
			if got := req.Header.Get(apiKeyHeader); got != "" {
				t.Errorf("recovered local request carried API key %q", got)
			}
			assertEVPIdentity(t, req)
			return newResponse(http.StatusAccepted), nil
		default:
			t.Fatalf("unexpected local path %q", req.URL.Path)
			return nil, errors.New("unexpected local path")
		}
	})}
	client.directClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		directEvents.Add(1)
		if got := req.Header.Get(apiKeyHeader); got != testAPIKey {
			t.Errorf("direct API key = %q, want configured key", got)
		}
		if got := req.Header.Get(evpSubdomainHeader); got != "" {
			t.Errorf("direct request carried local EVP header %q", got)
		}
		assertEVPIdentity(t, req)
		return newResponse(http.StatusAccepted), nil
	})}

	for range 2 {
		if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	if infoRequests.Load() != 1 || directEvents.Load() != 2 || localEvents.Load() != 0 {
		t.Fatalf("requests before recovery: info=%d local=%d direct=%d", infoRequests.Load(), localEvents.Load(), directEvents.Load())
	}

	recovered.Store(true)
	clock.Advance(time.Minute)
	if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if infoRequests.Load() != 2 || localEvents.Load() != 1 || directEvents.Load() != 2 {
		t.Fatalf("requests after recovery: info=%d local=%d direct=%d", infoRequests.Load(), localEvents.Load(), directEvents.Load())
	}
}

func TestAgentlessEVPRecoveryDiscoveryRunsOnceConcurrently(t *testing.T) {
	var infoRequests, eventRequests atomic.Int64
	var recovered atomic.Bool
	discoveryStarted := make(chan struct{})
	releaseDiscovery := make(chan struct{})

	client := newAgentlessEVPClient(internalffe.Settings{Source: internalffe.SourceAgentless})
	clock := useFakeEVPClock(client)
	client.agentURL = &url.URL{Scheme: "http", Host: "local.invalid"}
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/info" {
			requestNumber := infoRequests.Add(1)
			endpoints := `{"endpoints":[]}`
			if recovered.Load() {
				if requestNumber == 2 {
					close(discoveryStarted)
					<-releaseDiscovery
				}
				endpoints = `{"endpoints":["/evp_proxy/v2/"]}`
			}
			resp := newResponse(http.StatusOK)
			resp.Body = io.NopCloser(strings.NewReader(endpoints))
			return resp, nil
		}
		eventRequests.Add(1)
		return newResponse(http.StatusAccepted), nil
	})}

	if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); !errors.Is(err, errNoEVPRoute) {
		t.Fatalf("initial postRaw() error = %v, want %v", err, errNoEVPRoute)
	}
	recovered.Store(true)
	clock.Advance(time.Minute)

	const goroutines = 32
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for range goroutines {
		wg.Go(func() {
			errs <- client.postRaw(exposureEndpoint, "exposure", []byte(`{}`))
		})
	}
	<-discoveryStarted
	close(releaseDiscovery)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if infoRequests.Load() != 2 || eventRequests.Load() != goroutines {
		t.Fatalf("requests: info=%d event=%d, want info=2 event=%d", infoRequests.Load(), eventRequests.Load(), goroutines)
	}
}

func TestAgentlessEVPCanceledRecoveryProbeDoesNotPoisonRoute(t *testing.T) {
	var infoRequests, localEvents, directEvents atomic.Int64
	discoveryStarted := make(chan struct{})

	client := newAgentlessEVPClient(internalffe.Settings{
		Source: internalffe.SourceAgentless,
		Site:   "mock-intake.invalid",
		APIKey: testAPIKey,
	})
	clock := useFakeEVPClock(client)
	client.routeMode = evpRouteDirect
	client.recoverAt = clock.Now()
	client.agentURL = &url.URL{Scheme: "http", Host: "local.invalid"}
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/info":
			if infoRequests.Add(1) == 1 {
				close(discoveryStarted)
				<-req.Context().Done()
				return nil, req.Context().Err()
			}
			resp := newResponse(http.StatusOK)
			resp.Body = io.NopCloser(strings.NewReader(`{"endpoints":["/evp_proxy/v2/"]}`))
			return resp, nil
		case joinEVPPath(evpProxyV2Path, exposureEndpoint):
			localEvents.Add(1)
			return newResponse(http.StatusAccepted), nil
		default:
			t.Fatalf("unexpected local path %q", req.URL.Path)
			return nil, errors.New("unexpected local path")
		}
	})}
	client.directClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		directEvents.Add(1)
		return newResponse(http.StatusAccepted), nil
	})}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- client.postRawWithContext(ctx, exposureEndpoint, "exposure", []byte(`{}`))
	}()
	<-discoveryStarted
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled recovery error = %v, want %v", err, context.Canceled)
	}

	client.routeMu.RLock()
	mode, recoverAt := client.routeMode, client.recoverAt
	client.routeMu.RUnlock()
	if mode != evpRouteDirect || !recoverAt.Equal(clock.Now()) {
		t.Fatalf("route after canceled recovery = (%v, %v), want unchanged direct route at %v", mode, recoverAt, clock.Now())
	}

	if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err != nil {
		t.Fatalf("live recovery after canceled leader: %v", err)
	}
	if infoRequests.Load() != 2 || localEvents.Load() != 1 || directEvents.Load() != 0 {
		t.Fatalf("requests: info=%d local=%d direct=%d, want 2, 1, 0", infoRequests.Load(), localEvents.Load(), directEvents.Load())
	}
}

func TestAgentlessEVPRecoveryWaiterHonorsContext(t *testing.T) {
	var infoRequests, localEvents, directEvents atomic.Int64
	discoveryStarted := make(chan struct{})
	releaseDiscovery := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseDiscovery) })

	client := newAgentlessEVPClient(internalffe.Settings{
		Source: internalffe.SourceAgentless,
		Site:   "mock-intake.invalid",
		APIKey: testAPIKey,
	})
	clock := useFakeEVPClock(client)
	client.routeMode = evpRouteDirect
	client.recoverAt = clock.Now()
	client.agentURL = &url.URL{Scheme: "http", Host: "local.invalid"}
	client.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/info":
			infoRequests.Add(1)
			close(discoveryStarted)
			<-releaseDiscovery
			resp := newResponse(http.StatusOK)
			resp.Body = io.NopCloser(strings.NewReader(`{"endpoints":["/evp_proxy/v2/"]}`))
			return resp, nil
		case joinEVPPath(evpProxyV2Path, exposureEndpoint):
			localEvents.Add(1)
			return newResponse(http.StatusAccepted), nil
		default:
			t.Fatalf("unexpected local path %q", req.URL.Path)
			return nil, errors.New("unexpected local path")
		}
	})}
	client.directClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		directEvents.Add(1)
		return newResponse(http.StatusAccepted), nil
	})}

	leaderResult := make(chan error, 1)
	go func() {
		leaderResult <- client.postRaw(exposureEndpoint, "exposure", []byte(`{}`))
	}()
	<-discoveryStarted

	waiterCtx, waiterCancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer waiterCancel()
	started := time.Now()
	waiterErr := client.postRawWithContext(waiterCtx, exposureEndpoint, "exposure", []byte(`{}`))
	if !errors.Is(waiterErr, context.DeadlineExceeded) {
		t.Fatalf("waiting recovery error = %v, want %v", waiterErr, context.DeadlineExceeded)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("waiting recovery honored deadline after %v, want under 500ms", elapsed)
	}

	releaseOnce.Do(func() { close(releaseDiscovery) })
	if err := <-leaderResult; err != nil {
		t.Fatalf("recovery leader: %v", err)
	}
	if infoRequests.Load() != 1 || localEvents.Load() != 1 || directEvents.Load() != 0 {
		t.Fatalf("requests: info=%d local=%d direct=%d, want 1, 1, 0", infoRequests.Load(), localEvents.Load(), directEvents.Load())
	}
}

func TestAgentlessEVPDiscoveryRunsOnceConcurrently(t *testing.T) {
	var infoRequests, eventRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			infoRequests.Add(1)
			_, _ = io.WriteString(w, `{"endpoints":["/evp_proxy/v2/"]}`)
			return
		}
		eventRequests.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	agentURL, _ := url.Parse(server.URL)
	client := newAgentlessEVPClient(internalffe.Settings{Source: internalffe.SourceAgentless})
	client.agentURL = agentURL
	client.httpClient = server.Client()

	const goroutines = 16
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for range goroutines {
		wg.Go(func() {
			errs <- client.postRaw(exposureEndpoint, "exposure", []byte(`{}`))
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if infoRequests.Load() != 1 || eventRequests.Load() != goroutines {
		t.Fatalf("requests: info=%d event=%d, want info=1 event=%d", infoRequests.Load(), eventRequests.Load(), goroutines)
	}
}

func TestAgentlessEVPDirectClientUsesEnvironmentProxy(t *testing.T) {
	client := newAgentlessEVPClient(internalffe.Settings{
		Source: internalffe.SourceAgentless,
		Site:   "datadoghq.com",
		APIKey: testAPIKey,
	})
	transport, ok := client.directClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("direct transport type = %T, want *http.Transport", client.directClient.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("direct transport does not honor HTTP(S)_PROXY and NO_PROXY")
	}
}

func TestAgentlessEVPDirectClientDoesNotFollowRedirects(t *testing.T) {
	for _, status := range []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var redirectTargetRequests atomic.Int64
			redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				redirectTargetRequests.Add(1)
				if got := r.Header.Get(apiKeyHeader); got != "" {
					t.Errorf("redirect target received direct API key %q", got)
				}
				w.WriteHeader(http.StatusAccepted)
			}))
			defer redirectTarget.Close()

			redirectingIntake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get(apiKeyHeader); got != testAPIKey {
					t.Errorf("direct intake API key = %q, want configured key", got)
				}
				http.Redirect(w, r, redirectTarget.URL, status)
			}))
			defer redirectingIntake.Close()

			client := newAgentlessEVPClient(internalffe.Settings{
				Source: internalffe.SourceAgentless,
				Site:   "mock-intake.invalid",
				APIKey: testAPIKey,
			})
			client.directURL, _ = url.Parse(redirectingIntake.URL)
			client.directClient.Transport = redirectingIntake.Client().Transport
			client.selectDirect()

			err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`))
			var statusErr *evpHTTPStatusError
			if !errors.As(err, &statusErr) || statusErr.statusCode != status {
				t.Fatalf("postRaw() error = %v, want HTTP status %d", err, status)
			}
			if got := redirectTargetRequests.Load(); got != 0 {
				t.Fatalf("redirect target received %d request(s), want 0", got)
			}
		})
	}
}

func TestLocalEVPClientDoesNotFollowRedirects(t *testing.T) {
	var redirectTargetRequests atomic.Int64
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectTargetRequests.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer redirectTarget.Close()

	redirectingRelay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(apiKeyHeader); got != "" {
			t.Errorf("local relay received direct API key %q", got)
		}
		http.Redirect(w, r, redirectTarget.URL, http.StatusTemporaryRedirect)
	}))
	defer redirectingRelay.Close()

	client := newEVPClient()
	client.agentURL, _ = url.Parse(redirectingRelay.URL)
	client.httpClient.Transport = redirectingRelay.Client().Transport

	err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`))
	var statusErr *evpHTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.statusCode != http.StatusTemporaryRedirect {
		t.Fatalf("postRaw() error = %v, want HTTP status %d", err, http.StatusTemporaryRedirect)
	}
	if got := redirectTargetRequests.Load(); got != 0 {
		t.Fatalf("redirect target received %d request(s), want 0", got)
	}
}

func TestAgentlessEVPRejectsUnknownPathBeforeTransport(t *testing.T) {
	var requests atomic.Int64
	client := configuredAgentlessEVP(
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			return newResponse(http.StatusAccepted), nil
		}),
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			return newResponse(http.StatusAccepted), nil
		}),
	)

	err := client.postRaw("/api/v2/not-allowlisted", "unknown", []byte(`{}`))
	if !errors.Is(err, errInvalidEVPPath) {
		t.Fatalf("postRaw() error = %v, want %v", err, errInvalidEVPPath)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("unknown path reached transport %d time(s), want 0", got)
	}
}

func TestAgentlessEVPDirectTransportSendsEachBatchOnce(t *testing.T) {
	var requests atomic.Int64
	client := configuredAgentlessEVP(
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("local transport must not be used")
			return nil, errors.New("unexpected local request")
		}),
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			return nil, context.DeadlineExceeded
		}),
	)
	client.selectDirect()

	if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{"batch":1}`)); err == nil {
		t.Fatal("direct transport failure unexpectedly succeeded")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("direct batch attempts = %d, want exactly 1", got)
	}
}

func TestEVPHTTPStatusErrorDoesNotLogResponseBody(t *testing.T) {
	const sentinel = "sensitive-subject-and-api-key-sentinel"
	recorder := new(internallog.RecordLogger)
	undo := internallog.UseLogger(recorder)
	defer undo()

	client := configuredAgentlessEVP(
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			resp := newResponse(http.StatusInternalServerError)
			resp.Body = io.NopCloser(strings.NewReader(sentinel))
			return resp, nil
		}),
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("status 500 must not fall back to direct intake")
			return nil, errors.New("unexpected direct request")
		}),
	)
	err := client.postRaw(exposureEndpoint, "exposure", []byte(`{"batch":1}`))
	if err == nil {
		t.Fatal("status 500 unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("transport error exposed response body: %v", err)
	}
	internallog.Flush()
	logs := strings.Join(recorder.Logs(), "\n")
	if strings.Contains(logs, sentinel) {
		t.Fatalf("logs exposed response body: %s", logs)
	}
}

func TestDefinitivePreSendErrors(t *testing.T) {
	for _, err := range []error{
		syscall.ECONNREFUSED,
		syscall.ENOENT,
		&url.Error{Err: &net.DNSError{Err: "not found", Name: "missing.invalid", IsNotFound: true}},
		&url.Error{Err: &net.DNSError{Err: "temporary", Name: "retry.invalid", IsTemporary: true}},
	} {
		if !isDefinitivePreSendError(err) {
			t.Errorf("error %v was not classified as definitive", err)
		}
	}
	for _, err := range []error{syscall.ECONNRESET, syscall.EPIPE, context.DeadlineExceeded, errors.New("unknown")} {
		if isDefinitivePreSendError(err) {
			t.Errorf("error %v was classified as definitive", err)
		}
	}
}

func TestEVPClientPostRawRequest(t *testing.T) {
	body := []byte(`{"context":{"service":"test-service"},"flagEvaluations":[]}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: got %q, want %q", r.Method, http.MethodPost)
		}
		wantPath := joinEVPPath(evpProxyV2Path, flagEvalLoggingEndpoint)
		if r.URL.Path != wantPath {
			t.Errorf("unexpected path: got %q, want %q", r.URL.Path, wantPath)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("unexpected Content-Type header: got %q, want %q", got, "application/json")
		}
		if got := r.Header.Get(evpSubdomainHeader); got != evpSubdomainValue {
			t.Errorf("unexpected %s header: got %q, want %q", evpSubdomainHeader, got, evpSubdomainValue)
		}
		if got := r.Header.Get(headerEVPOrigin); got != evpOrigin {
			t.Errorf("unexpected %s header: got %q, want %q", headerEVPOrigin, got, evpOrigin)
		}
		if got := r.Header.Get(headerEVPOriginVersion); got != version.Tag {
			t.Errorf("unexpected %s header: got %q, want %q", headerEVPOriginVersion, got, version.Tag)
		}

		gotBody, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read request body: %v", err)
		} else if !bytes.Equal(gotBody, body) {
			t.Errorf("unexpected body: got %q, want %q", gotBody, body)
		}

		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	agentURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("failed to parse test server URL: %v", err)
	}

	client := newEVPClient()
	client.httpClient = server.Client()
	client.agentURL = agentURL
	if err := client.postRaw(flagEvalLoggingEndpoint, "flag evaluation", body); err != nil {
		t.Fatalf("postRaw returned an error: %v", err)
	}
}
