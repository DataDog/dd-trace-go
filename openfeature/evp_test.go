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

	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

const compatibleEVPProxyHeadersJSON = `"evp_proxy_allowed_headers":["DD-EVP-ORIGIN","DD-EVP-ORIGIN-VERSION"]`

func TestBuildDirectEVPURL(t *testing.T) {
	tests := map[string]struct {
		site string
		want string
	}{
		"default": {
			want: "https://event-platform-intake.datadoghq.com",
		},
		"lowercase": {
			site: "DATADOGHQ.EU",
			want: "https://event-platform-intake.datadoghq.eu",
		},
		"leading whitespace": {
			site: " datadoghq.com",
			want: "https://event-platform-intake.datadoghq.com",
		},
		"trailing whitespace": {
			site: "datadoghq.com\t",
			want: "https://event-platform-intake.datadoghq.com",
		},
		"whitespace only uses default": {
			site: " \t",
			want: "https://event-platform-intake.datadoghq.com",
		},
		"scheme": {
			site: "https://datadoghq.com",
		},
		"unicode before lowercase": {
			site: "K.com",
		},
		"backslash": {
			site: `datadoghq.com\attacker`,
		},
		"query": {
			site: "datadoghq.com?next=example.com",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := buildDirectEVPURL(tt.site)
			if tt.want == "" {
				if got != nil {
					t.Fatalf("buildDirectEVPURL(%q) = %q, want nil", tt.site, got)
				}
				return
			}
			if got == nil || got.String() != tt.want {
				t.Fatalf("buildDirectEVPURL(%q) = %v, want %q", tt.site, got, tt.want)
			}
		})
	}
}

func TestNewAgentlessEVPClientWarnsOnInvalidSite(t *testing.T) {
	logger, undo := newCapturingLogger()
	defer undo()

	const apiKey = "must-not-be-logged"
	c := newAgentlessEVPClient(internalffe.Settings{APIKey: apiKey, Site: "K.com"})
	if c.directURL != nil || c.directClient != nil {
		t.Fatal("invalid site configured a direct EVP client")
	}
	if got := logger.countContaining("DD_SITE is invalid"); got != 1 {
		t.Fatalf("invalid-site warnings = %d, want 1", got)
	}
	if got := logger.countContaining(apiKey); got != 0 {
		t.Fatal("invalid-site warning leaked the API key")
	}
}

func TestAgentlessEVPRouteSelectionAndCredentials(t *testing.T) {
	tests := map[string]struct {
		endpoints  []string
		wantLocal  string
		wantDirect bool
	}{
		"prefers v4": {
			endpoints: []string{evpProxyV2Path, evpProxyV4Path},
			wantLocal: evpProxyV4Path + exposureEndpoint,
		},
		"uses v2": {
			endpoints: []string{evpProxyV2Path + "/"},
			wantLocal: evpProxyV2Path + exposureEndpoint,
		},
		"falls back direct": {
			endpoints:  []string{"/evp_proxy/v1"},
			wantDirect: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var localPath string
			var localHeader http.Header
			agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/info" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"endpoints":["`+strings.Join(tt.endpoints, `","`)+`"],`+compatibleEVPProxyHeadersJSON+`}`)
					return
				}
				localPath = r.URL.Path
				localHeader = r.Header.Clone()
				w.WriteHeader(http.StatusAccepted)
			}))
			defer agent.Close()

			var directPath string
			var directHeader http.Header
			direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				directPath = r.URL.Path
				directHeader = r.Header.Clone()
				w.WriteHeader(http.StatusAccepted)
			}))
			defer direct.Close()

			c := testAgentlessEVPClient(t, agent, direct, "api-key")
			if err := c.postRaw(exposureEndpoint, "exposure", []byte(`{"ok":true}`)); err != nil {
				t.Fatal(err)
			}

			if tt.wantDirect {
				if directPath != exposureEndpoint {
					t.Fatalf("direct path = %q, want %q", directPath, exposureEndpoint)
				}
				if got := directHeader.Get(apiKeyHeader); got != "api-key" {
					t.Fatalf("direct API key = %q, want api-key", got)
				}
				if got := directHeader.Get(evpSubdomainHeader); got != "" {
					t.Fatalf("direct request leaked local routing header %q", got)
				}
				assertEVPIdentityHeaders(t, directHeader)
				if localPath != "" {
					t.Fatalf("unexpected local event request to %q", localPath)
				}
				return
			}

			if localPath != tt.wantLocal {
				t.Fatalf("local path = %q, want %q", localPath, tt.wantLocal)
			}
			if got := localHeader.Get(evpSubdomainHeader); got != evpSubdomainValue {
				t.Fatalf("local routing header = %q, want %q", got, evpSubdomainValue)
			}
			if got := localHeader.Get(apiKeyHeader); got != "" {
				t.Fatalf("local request leaked API key %q", got)
			}
			assertEVPIdentityHeaders(t, localHeader)
			if directPath != "" {
				t.Fatalf("unexpected direct event request to %q", directPath)
			}
		})
	}
}

func TestAgentlessEVPRequiresIdentityHeaderForwarding(t *testing.T) {
	tests := map[string]struct {
		infoJSON  string
		wantLocal bool
	}{
		"missing capability": {
			infoJSON: `{"endpoints":["/evp_proxy/v4"]}`,
		},
		"null capability": {
			infoJSON: `{"endpoints":["/evp_proxy/v4"],"evp_proxy_allowed_headers":null}`,
		},
		"origin only": {
			infoJSON: `{"endpoints":["/evp_proxy/v4"],"evp_proxy_allowed_headers":["DD-EVP-ORIGIN"]}`,
		},
		"origin version only": {
			infoJSON: `{"endpoints":["/evp_proxy/v4"],"evp_proxy_allowed_headers":["DD-EVP-ORIGIN-VERSION"]}`,
		},
		"both case insensitive": {
			infoJSON:  `{"endpoints":["/evp_proxy/v4"],"evp_proxy_allowed_headers":["dd-evp-origin-version","dd-evp-origin"]}`,
			wantLocal: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			var localPosts atomic.Int32
			agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/info" {
					_, _ = io.WriteString(w, tt.infoJSON)
					return
				}
				localPosts.Add(1)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer agent.Close()

			var directPosts atomic.Int32
			direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				directPosts.Add(1)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer direct.Close()

			c := testAgentlessEVPClient(t, agent, direct, "api-key")
			if err := c.postRaw(exposureEndpoint, "exposure", nil); err != nil {
				t.Fatal(err)
			}

			if tt.wantLocal {
				if got := localPosts.Load(); got != 1 {
					t.Fatalf("local posts = %d, want 1", got)
				}
				if got := directPosts.Load(); got != 0 {
					t.Fatalf("direct posts = %d, want 0", got)
				}
				return
			}
			if got := localPosts.Load(); got != 0 {
				t.Fatalf("local posts = %d, want 0", got)
			}
			if got := directPosts.Load(); got != 1 {
				t.Fatalf("direct posts = %d, want 1", got)
			}
		})
	}
}

func TestAgentlessEVPPreservesAgentURLPath(t *testing.T) {
	const agentBasePath = "/agent-prefix"
	var infoCalls atomic.Int32
	var eventCalls atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case agentBasePath + "/info":
			infoCalls.Add(1)
			_, _ = io.WriteString(w, `{"endpoints":["/evp_proxy/v4"],`+compatibleEVPProxyHeadersJSON+`}`)
		case agentBasePath + evpProxyV4Path + exposureEndpoint:
			eventCalls.Add(1)
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer agent.Close()

	c := testAgentlessEVPClient(t, agent, nil, "")
	agentURL, err := url.Parse(agent.URL + agentBasePath + "/")
	if err != nil {
		t.Fatal(err)
	}
	c.agentURL = agentURL
	if err := c.postRaw(exposureEndpoint, "exposure", nil); err != nil {
		t.Fatal(err)
	}
	if got := infoCalls.Load(); got != 1 {
		t.Fatalf("prefixed /info calls = %d, want 1", got)
	}
	if got := eventCalls.Load(); got != 1 {
		t.Fatalf("prefixed event calls = %d, want 1", got)
	}
}

func TestAgentlessEVPNoRouteRecoversAfterCooldown(t *testing.T) {
	var ready atomic.Bool
	var infoCalls atomic.Int32
	var eventCalls atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			infoCalls.Add(1)
			if ready.Load() {
				_, _ = io.WriteString(w, `{"endpoints":["/evp_proxy/v2"],`+compatibleEVPProxyHeadersJSON+`}`)
			} else {
				_, _ = io.WriteString(w, `{"endpoints":[]}`)
			}
			return
		}
		eventCalls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer agent.Close()

	c := testAgentlessEVPClient(t, agent, nil, "")
	now := time.Unix(100, 0)
	c.now = func() time.Time { return now }
	c.cooldown = time.Minute

	if err := c.postRaw(exposureEndpoint, "exposure", nil); !errors.Is(err, errNoEVPRoute) {
		t.Fatalf("first post error = %v, want %v", err, errNoEVPRoute)
	}
	ready.Store(true)
	if err := c.postRaw(exposureEndpoint, "exposure", nil); !errors.Is(err, errNoEVPRoute) {
		t.Fatalf("post during cooldown error = %v, want %v", err, errNoEVPRoute)
	}
	if got := infoCalls.Load(); got != 1 {
		t.Fatalf("/info calls during cooldown = %d, want 1", got)
	}

	now = now.Add(time.Minute)
	if err := c.postRaw(exposureEndpoint, "exposure", nil); err != nil {
		t.Fatalf("post after Agent recovery: %v", err)
	}
	if got := infoCalls.Load(); got != 2 {
		t.Fatalf("/info calls after cooldown = %d, want 2", got)
	}
	if got := eventCalls.Load(); got != 1 {
		t.Fatalf("local event calls = %d, want 1", got)
	}
}

func TestAgentlessEVPRejectedLocalRouteReplaysDirect(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/info" {
					_, _ = io.WriteString(w, `{"endpoints":["/evp_proxy/v4"],`+compatibleEVPProxyHeadersJSON+`}`)
					return
				}
				w.WriteHeader(status)
			}))
			defer agent.Close()

			var directCalls atomic.Int32
			direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				directCalls.Add(1)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer direct.Close()

			c := testAgentlessEVPClient(t, agent, direct, "api-key")
			if err := c.postRaw(exposureEndpoint, "exposure", nil); err != nil {
				t.Fatalf("post after local %d: %v", status, err)
			}
			if got := directCalls.Load(); got != 1 {
				t.Fatalf("direct calls = %d, want 1", got)
			}
		})
	}
}

func TestAgentlessEVPDoesNotReplayAmbiguousResponses(t *testing.T) {
	for _, status := range []int{
		http.StatusForbidden,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			const secret = "response-body-must-not-escape"
			agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/info" {
					_, _ = io.WriteString(w, `{"endpoints":["/evp_proxy/v2"],`+compatibleEVPProxyHeadersJSON+`}`)
					return
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, secret)
			}))
			defer agent.Close()

			var directCalls atomic.Int32
			direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				directCalls.Add(1)
				w.WriteHeader(http.StatusAccepted)
			}))
			defer direct.Close()

			c := testAgentlessEVPClient(t, agent, direct, "api-key")
			err := c.postRaw(exposureEndpoint, "exposure", nil)
			if err == nil {
				t.Fatalf("post returned nil, want status %d error", status)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error exposed response body: %v", err)
			}
			if got := directCalls.Load(); got != 0 {
				t.Fatalf("direct calls = %d, want 0", got)
			}
			if err := c.postRaw(exposureEndpoint, "exposure", nil); err != nil {
				t.Fatalf("future post through direct route: %v", err)
			}
			if got := directCalls.Load(); got != 1 {
				t.Fatalf("future direct calls = %d, want 1", got)
			}
		})
	}
}

func TestAgentlessEVPNonReplayableResponseWithoutCredentialsEntersCooldown(t *testing.T) {
	var ready atomic.Bool
	var infoCalls atomic.Int32
	var eventCalls atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			infoCalls.Add(1)
			_, _ = io.WriteString(w, `{"endpoints":["/evp_proxy/v2"],`+compatibleEVPProxyHeadersJSON+`}`)
			return
		}
		eventCalls.Add(1)
		if ready.Load() {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer agent.Close()

	c := testAgentlessEVPClient(t, agent, nil, "")
	now := time.Unix(100, 0)
	c.now = func() time.Time { return now }
	c.cooldown = time.Minute

	if err := c.postRaw(exposureEndpoint, "exposure", nil); err == nil {
		t.Fatal("first post returned nil, want status error")
	}
	ready.Store(true)
	if err := c.postRaw(exposureEndpoint, "exposure", nil); !errors.Is(err, errNoEVPRoute) {
		t.Fatalf("post during cooldown error = %v, want %v", err, errNoEVPRoute)
	}
	if got := eventCalls.Load(); got != 1 {
		t.Fatalf("local posts during cooldown = %d, want 1", got)
	}

	now = now.Add(time.Minute)
	if err := c.postRaw(exposureEndpoint, "exposure", nil); err != nil {
		t.Fatalf("post after cooldown: %v", err)
	}
	if got := infoCalls.Load(); got != 2 {
		t.Fatalf("/info calls = %d, want 2", got)
	}
	if got := eventCalls.Load(); got != 2 {
		t.Fatalf("local posts after recovery = %d, want 2", got)
	}
}

func TestEVPClientDrainsErrorResponseBodyForConnectionReuse(t *testing.T) {
	var newConnections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, strings.Repeat("x", 1024))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			newConnections.Add(1)
		}
	}
	server.Start()
	defer server.Close()

	agentURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := newEVPClient()
	c.agentURL = agentURL
	c.httpClient = server.Client()

	for range 2 {
		err := c.postRaw(exposureEndpoint, "exposure", nil)
		var statusErr *evpHTTPStatusError
		if !errors.As(err, &statusErr) || statusErr.statusCode != http.StatusInternalServerError {
			t.Fatalf("post error = %v, want status %d", err, http.StatusInternalServerError)
		}
	}
	if got := newConnections.Load(); got != 1 {
		t.Fatalf("new connections = %d, want 1", got)
	}
}

func TestAgentlessEVPTransportFailureChangesOnlyFutureRouting(t *testing.T) {
	var localPosts atomic.Int32
	agentURL, err := url.Parse("http://agent.invalid")
	if err != nil {
		t.Fatal(err)
	}
	agentClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/info" {
			return response(http.StatusOK, `{"endpoints":["/evp_proxy/v2"],`+compatibleEVPProxyHeadersJSON+`}`), nil
		}
		localPosts.Add(1)
		return nil, errors.New("ambiguous transport failure")
	})}

	var directPosts atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		directPosts.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer direct.Close()
	directURL, err := url.Parse(direct.URL)
	if err != nil {
		t.Fatal(err)
	}

	c := newEVPClientBase()
	c.agentURL = agentURL
	c.httpClient = agentClient
	c.directURL = directURL
	c.directClient = direct.Client()
	c.apiKey = "api-key"

	if err := c.postRaw(exposureEndpoint, "exposure", nil); err == nil {
		t.Fatal("first post returned nil, want transport error")
	}
	if got := directPosts.Load(); got != 0 {
		t.Fatalf("first post was replayed direct %d times", got)
	}
	if err := c.postRaw(exposureEndpoint, "exposure", nil); err != nil {
		t.Fatalf("second post through direct route: %v", err)
	}
	if got := localPosts.Load(); got != 1 {
		t.Fatalf("local posts = %d, want 1", got)
	}
	if got := directPosts.Load(); got != 1 {
		t.Fatalf("direct posts = %d, want 1", got)
	}
}

func TestAgentlessEVPDefinitivePreSendFailureReplaysDirect(t *testing.T) {
	agentURL, err := url.Parse("http://agent.invalid")
	if err != nil {
		t.Fatal(err)
	}
	agentClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/info" {
			return response(http.StatusOK, `{"endpoints":["/evp_proxy/v2"],`+compatibleEVPProxyHeadersJSON+`}`), nil
		}
		return nil, syscall.ECONNREFUSED
	})}

	var directPosts atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		directPosts.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer direct.Close()
	directURL, err := url.Parse(direct.URL)
	if err != nil {
		t.Fatal(err)
	}

	c := newEVPClientBase()
	c.agentURL = agentURL
	c.httpClient = agentClient
	c.directURL = directURL
	c.directClient = direct.Client()
	c.apiKey = "api-key"

	if err := c.postRaw(exposureEndpoint, "exposure", []byte(`{"batch":1}`)); err != nil {
		t.Fatalf("post after definitive pre-send failure: %v", err)
	}
	if got := directPosts.Load(); got != 1 {
		t.Fatalf("direct posts = %d, want 1", got)
	}
}

func TestAgentlessEVPDoesNotReplayWrittenRequest(t *testing.T) {
	agentURL, err := url.Parse("http://agent.invalid")
	if err != nil {
		t.Fatal(err)
	}
	agentClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/info" {
			return response(http.StatusOK, `{"endpoints":["/evp_proxy/v2"],`+compatibleEVPProxyHeadersJSON+`}`), nil
		}
		if trace := httptrace.ContextClientTrace(r.Context()); trace != nil && trace.WroteRequest != nil {
			trace.WroteRequest(httptrace.WroteRequestInfo{})
		}
		return nil, syscall.ECONNREFUSED
	})}

	var directPosts atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		directPosts.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer direct.Close()
	directURL, err := url.Parse(direct.URL)
	if err != nil {
		t.Fatal(err)
	}

	c := newEVPClientBase()
	c.agentURL = agentURL
	c.httpClient = agentClient
	c.directURL = directURL
	c.directClient = direct.Client()
	c.apiKey = "api-key"

	if err := c.postRaw(exposureEndpoint, "exposure", []byte(`{"batch":1}`)); err == nil {
		t.Fatal("written local request unexpectedly succeeded")
	}
	if got := directPosts.Load(); got != 0 {
		t.Fatalf("written request was replayed direct %d times", got)
	}
}

func TestDefinitivePreSendErrors(t *testing.T) {
	for _, err := range []error{
		syscall.ECONNREFUSED,
		&url.Error{Err: windowsWSAECONNREFUSED},
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

func TestAgentlessEVPDirectRedirectDoesNotForwardAPIKey(t *testing.T) {
	var targetCalls atomic.Int32
	var targetAPIKey atomic.Value
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		targetAPIKey.Store(r.Header.Get(apiKeyHeader))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	redirectURL, err := url.Parse(redirect.URL)
	if err != nil {
		t.Fatal(err)
	}

	c := newAgentlessEVPClient(internalffe.Settings{
		APIKey: "api-key",
		Site:   "datadoghq.com",
	})
	c.directURL = redirectURL
	c.routeMode = evpRouteDirect

	err = c.postRaw(exposureEndpoint, "exposure", nil)
	var statusErr *evpHTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.statusCode != http.StatusTemporaryRedirect {
		t.Fatalf("redirect error = %v, want status %d", err, http.StatusTemporaryRedirect)
	}
	if got := targetCalls.Load(); got != 0 {
		t.Fatalf("redirect target calls = %d, want 0", got)
	}
	if got := targetAPIKey.Load(); got != nil {
		t.Fatalf("redirect target received API key %q", got)
	}
}

func TestAgentlessEVPSerializesInitialDiscovery(t *testing.T) {
	var infoCalls atomic.Int32
	var eventCalls atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			infoCalls.Add(1)
			_, _ = io.WriteString(w, `{"endpoints":["/evp_proxy/v4"],`+compatibleEVPProxyHeadersJSON+`}`)
			return
		}
		eventCalls.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer agent.Close()

	c := testAgentlessEVPClient(t, agent, nil, "")
	const callers = 12
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Go(func() {
			errs <- c.postRaw(exposureEndpoint, "exposure", nil)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := infoCalls.Load(); got != 1 {
		t.Fatalf("/info calls = %d, want 1", got)
	}
	if got := eventCalls.Load(); got != callers {
		t.Fatalf("event calls = %d, want %d", got, callers)
	}
}

func TestAgentOnlyEVPClientKeepsV2Route(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotHeaders http.Header
	var gotBody []byte
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read request body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer agent.Close()
	t.Setenv("DD_TRACE_AGENT_URL", agent.URL+"/agent-prefix")

	body := []byte(`{"context":{"service":"test-service"},"flagEvaluations":[]}`)
	c := newEVPClient()
	if err := c.postRaw(flagEvalLoggingEndpoint, "flag evaluation", body); err != nil {
		t.Fatal(err)
	}
	wantPath := "/agent-prefix" + evpProxyV2Path + flagEvalLoggingEndpoint
	if gotPath != wantPath {
		t.Fatalf("path = %q, want %q", gotPath, wantPath)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want %q", gotMethod, http.MethodPost)
	}
	if got := gotHeaders.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := gotHeaders.Get(evpSubdomainHeader); got != evpSubdomainValue {
		t.Fatalf("local routing header = %q, want %q", got, evpSubdomainValue)
	}
	if got := gotHeaders.Get(apiKeyHeader); got != "" {
		t.Fatalf("Agent-only request leaked API key %q", got)
	}
	assertEVPIdentityHeaders(t, gotHeaders)
	if !bytes.Equal(gotBody, body) {
		t.Fatalf("body = %q, want %q", gotBody, body)
	}
}

func TestAgentOnlyEVPClientKeepsV2RouteAfterFailure(t *testing.T) {
	for name, transportErr := range map[string]error{
		"rejected route":  nil,
		"transport error": syscall.ECONNREFUSED,
	} {
		t.Run(name, func(t *testing.T) {
			var calls int
			c := newEVPClient()
			c.agentURL = &url.URL{Scheme: "http", Host: "agent.invalid"}
			c.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != evpProxyV2Path+exposureEndpoint {
					t.Fatalf("request path = %q, want fixed v2 route", r.URL.Path)
				}
				calls++
				if calls == 1 {
					if transportErr != nil {
						return nil, transportErr
					}
					return response(http.StatusNotFound, ""), nil
				}
				return response(http.StatusAccepted, ""), nil
			})}

			if err := c.postRaw(exposureEndpoint, "exposure", nil); err == nil {
				t.Fatal("first post returned nil, want failure")
			}
			if err := c.postRaw(exposureEndpoint, "exposure", nil); err != nil {
				t.Fatalf("second fixed-v2 post failed: %v", err)
			}
			if calls != 2 {
				t.Fatalf("Agent calls = %d, want 2", calls)
			}
		})
	}
}

func testAgentlessEVPClient(t *testing.T, agent, direct *httptest.Server, apiKey string) *evpClient {
	t.Helper()
	c := newEVPClientBase()
	if agent != nil {
		agentURL, err := url.Parse(agent.URL)
		if err != nil {
			t.Fatal(err)
		}
		c.agentURL = agentURL
		c.httpClient = agent.Client()
	} else {
		c.agentURL = nil
		c.httpClient = nil
	}
	if direct != nil {
		directURL, err := url.Parse(direct.URL)
		if err != nil {
			t.Fatal(err)
		}
		c.directURL = directURL
		c.directClient = direct.Client()
	}
	c.apiKey = apiKey
	return c
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func assertEVPIdentityHeaders(t *testing.T, headers http.Header) {
	t.Helper()
	if got := headers.Get(headerEVPOrigin); got != evpOrigin {
		t.Errorf("%s = %q, want %q", headerEVPOrigin, got, evpOrigin)
	}
	if got := headers.Get(headerEVPOriginVersion); got != version.Tag {
		t.Errorf("%s = %q, want %q", headerEVPOriginVersion, got, version.Tag)
	}
}
