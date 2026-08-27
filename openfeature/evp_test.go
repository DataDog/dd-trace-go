// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
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

	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
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
		{name: "default site", site: "datadoghq.com", wantHost: "event-platform-intake.datadoghq.com"},
		{name: "custom domain", site: "custom.example", wantHost: "event-platform-intake.custom.example"},
		{name: "uppercase domain with outer whitespace", site: "  DATADOGHQ.EU\t", wantHost: "event-platform-intake.datadoghq.eu"},
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
		{name: "empty", site: ""},
		{name: "userinfo", site: "datadoghq.com@evil.example"},
		{name: "userinfo with password", site: "datadoghq.com:password@evil.example"},
		{name: "scheme", site: "https://datadoghq.com"},
		{name: "default port", site: "datadoghq.com:443"},
		{name: "custom port", site: "datadoghq.com:8443"},
		{name: "path", site: "datadoghq.com/path"},
		{name: "query", site: "datadoghq.com?query=value"},
		{name: "fragment", site: "datadoghq.com#fragment"},
		{name: "internal whitespace", site: "data doghq.com"},
		{name: "backslash", site: "datadoghq.com\\evil.example"},
		{name: "percent-encoded dot", site: "datadoghq.com%2eattacker.example"},
		{name: "ideographic full stop", site: "datadoghq.com。attacker.example"},
		{name: "fullwidth full stop", site: "datadoghq.com．attacker.example"},
		{name: "halfwidth ideographic full stop", site: "datadoghq.com｡attacker.example"},
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
		Site:   "  CUSTOM.EXAMPLE  ",
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
		return newResponse(http.StatusAccepted), nil
	})}

	if err := client.postRaw(exposureEndpoint, "exposure", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
}

func TestAgentlessEVPUnsafeSiteNeverReachesDirectTransport(t *testing.T) {
	for _, site := range []string{
		"datadoghq.com%2eattacker.example",
		"datadoghq.com。attacker.example",
		"datadoghq.com．attacker.example",
		"datadoghq.com｡attacker.example",
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

	t.Run("403 404 and 405 replay current batch direct", func(t *testing.T) {
		for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed} {
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

	t.Run("429 and 5xx remain local", func(t *testing.T) {
		for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
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
