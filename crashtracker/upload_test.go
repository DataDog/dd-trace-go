// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package crashtracker

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// decompressBody gunzips a captured request body for JSON assertions.
func decompressBody(t *testing.T, body []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("create gzip reader: %v", err)
	}
	defer r.Close()
	decompressed, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read gzip stream: %v", err)
	}
	return decompressed
}

// newTestReport builds a Report exercising uploadReport and its request builder
// in isolation from a real crash dump. DDTags and OSInfo are derived from the
// real buildDDTags/osInfo rather than hand-typed, so this fixture's shape
// tracks the actual wire contract instead of silently drifting from it.
func newTestReport() *Report {
	stack := StackTrace{
		Format: "Datadog Crashtracker 1.0",
		Frames: []Frame{{
			Function: "main.main",
			File:     "main.go",
			Line:     10,
		}},
	}
	r := &Report{
		Timestamp: 1700000000000,
		DDSource:  "crashtracker",
		Error: Error{
			Type:       "SIGSEGV",
			Message:    "segmentation fault",
			Stack:      &stack,
			Threads:    []Thread{{Crashed: true, Name: "goroutine 1", Stack: stack}},
			ThreadName: "goroutine 1",
			IsCrash:    true,
			SourceType: "Crashtracking",
		},
		OSInfo: osInfo(),
	}
	r.DDTags = buildDDTags(&config{}, r)
	return r
}

type testUploadTransport func(*http.Request) (*http.Response, error)

func (f testUploadTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	defer r.Body.Close()
	return f(r)
}

func TestUploadReportContextStopsRetryBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		arrived, reply := make(chan struct{}), make(chan struct{})
		requests := 0
		client := &http.Client{Transport: testUploadTransport(func(*http.Request) (*http.Response, error) {
			requests++
			if requests == 1 {
				close(arrived)
				<-reply
			}
			return &http.Response{StatusCode: http.StatusServiceUnavailable}, nil
		})}
		done := make(chan error, 1)
		go func() {
			done <- uploadReport(ctx, &config{agentURL: "http://intake.test", httpClient: client}, newTestReport())
		}()
		<-arrived
		close(reply)
		synctest.Wait()
		started := time.Now()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "intake returned 503") {
			t.Fatalf("uploadReport = %v, want cancellation after a 503", err)
		}
		if requests != 1 || time.Since(started) != 0 {
			t.Fatalf("canceled upload made %d requests and took %s", requests, time.Since(started))
		}
	})
}

func TestUploadReportContextDeadlineInterruptsRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		var requestErr error
		client := &http.Client{Transport: testUploadTransport(func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			requestErr = r.Context().Err()
			return nil, requestErr
		})}
		err := uploadReport(ctx, &config{agentURL: "http://intake.test", httpClient: client}, newTestReport())
		if !errors.Is(err, context.DeadlineExceeded) || requestErr != context.DeadlineExceeded {
			t.Fatalf("upload error = %v, transport error = %v, want deadline exceeded", err, requestErr)
		}
	})
}

func TestUploadTargetNewClientTimeout(t *testing.T) {
	for _, tt := range []struct {
		name   string
		target uploadTarget
		want   time.Duration
	}{
		{name: "Agent HTTP", target: uploadTarget{}, want: 3 * time.Second},
		{name: "agentless HTTPS", target: uploadTarget{useKey: true}, want: 5 * time.Second},
		{name: "Agent UDS", target: uploadTarget{socketPath: "/tmp/s"}, want: 3 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.target.newClient(&config{}).Timeout; got != tt.want {
				t.Errorf("client timeout = %s, want %s", got, tt.want)
			}
		})
	}

	t.Run("custom", func(t *testing.T) {
		const wantTimeout = 7 * time.Second
		custom := &http.Client{Timeout: wantTimeout}
		cfg := &config{}
		WithHTTPClient(custom)(cfg)
		got := (uploadTarget{useKey: true}).newClient(cfg)
		if got != custom {
			t.Fatalf("client = %p, want custom client %p", got, custom)
		}
		if got.Timeout != wantTimeout {
			t.Errorf("client timeout = %s, want %s", got.Timeout, wantTimeout)
		}
	})
}

func TestUploadReportRetriesHungFirstAttempt(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var attempts int
		var delivered []byte
		client := (uploadTarget{}).newClient(&config{})
		client.Transport = testUploadTransport(func(r *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
			var err error
			delivered, err = io.ReadAll(r.Body)
			return &http.Response{StatusCode: http.StatusAccepted}, err
		})
		ctx, cancel := context.WithTimeout(t.Context(), uploadDeadline)
		defer cancel()
		if err := uploadReport(ctx, &config{agentURL: "http://intake.test", httpClient: client}, newTestReport()); err != nil {
			t.Fatal(err)
		}
		if attempts != 2 {
			t.Fatalf("upload attempts = %d, want 2", attempts)
		}
		assertRFC0013Body(t, decompressBody(t, delivered))
	})
}

func TestUploadReportDeadlineErrorIncludesLastError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		client := &http.Client{Transport: testUploadTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusServiceUnavailable}, nil
		})}
		err := uploadReport(ctx, &config{agentURL: "http://intake.test", httpClient: client}, newTestReport())
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "intake returned 503") {
			t.Fatalf("uploadReport = %v, want deadline exceeded after a 503", err)
		}
	})
}

func TestUploadReportAgentPath(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		capturedBody = body
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	if err := uploadReport(t.Context(), cfg, newTestReport()); err != nil {
		t.Fatalf("uploadReport returned unexpected error: %v", err)
	}

	if capturedReq == nil {
		t.Fatal("no request captured by test server")
	}

	assertCanonicalAgentRequest(t, capturedReq)

	// Content-Type and Content-Encoding headers.
	if ct := capturedReq.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if ce := capturedReq.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", ce)
	}

	// API key must NOT be set on the agent path.
	if key := capturedReq.Header.Get("DD-API-KEY"); key != "" {
		t.Errorf("DD-API-KEY should be absent on agent path, got %q", key)
	}

	assertRFC0013Body(t, decompressBody(t, capturedBody))
}

func TestUploadReportAgentlessPath(t *testing.T) {
	var capturedReq *http.Request
	var capturedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		capturedBody = body
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := &config{
		apiKey:       "test-key",
		site:         "datadoghq.com",
		agentlessURL: srv.URL, // test-only override for the agentless target
		httpClient:   srv.Client(),
	}

	if err := uploadReport(t.Context(), cfg, newTestReport()); err != nil {
		t.Fatalf("uploadReport returned unexpected error: %v", err)
	}

	if capturedReq == nil {
		t.Fatal("no request captured by test server")
	}

	// DD-API-KEY header must be set.
	if key := capturedReq.Header.Get("DD-API-KEY"); key != "test-key" {
		t.Errorf("DD-API-KEY = %q, want test-key", key)
	}

	// EVP subdomain must NOT be set on agentless path.
	if sub := capturedReq.Header.Get("X-Datadog-EVP-Subdomain"); sub != "" {
		t.Errorf("X-Datadog-EVP-Subdomain should be absent on agentless path, got %q", sub)
	}

	assertRFC0013Body(t, decompressBody(t, capturedBody))
}

// TestUploadReportAgentURLIgnoredInAgentlessMode verifies the fix for the bug
// where WithAgentURL was read verbatim as the complete agentless target: a
// resolved cfg.agentURL (an agent host:port, no path) combined with an API
// key must not become the agentless request's target URL, which would 404 or
// otherwise fail to reach the intake path. The computed agentless URL is used
// instead; only the unexported test seam (agentlessURL) can override it.
func TestUploadReportAgentURLIgnoredInAgentlessMode(t *testing.T) {
	cfg := &config{
		apiKey:   "test-key",
		site:     "datadoghq.com",
		agentURL: "http://agent-host:8126", // must be ignored: this is an agent base, not an agentless target
	}

	req, err := resolveUploadTarget(cfg).newRequest(cfg, []byte("{}"))
	if err != nil {
		t.Fatalf("newRequest returned unexpected error: %v", err)
	}

	if req.URL.String() == cfg.agentURL {
		t.Fatalf("target URL = %q, want the computed agentless URL, not the agent URL verbatim", req.URL.String())
	}
	wantHost := "error-tracking-intake.datadoghq.com"
	if req.URL.Host != wantHost {
		t.Errorf("target host = %q, want %q", req.URL.Host, wantHost)
	}
}

func TestUploadReportServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	err := uploadReport(t.Context(), cfg, newTestReport())
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error %q does not mention status code 500", err.Error())
	}
}

// TestUploadReportRejects3xx verifies the fix for the bug where any status
// below 400 — including a 3xx client.Do did not or could not follow — was
// treated as a successful upload. 304 is a clean case to test: it is not in
// net/http's default set of client-followed redirect codes, so client.Do
// returns it as-is with err == nil, exactly the shape a misbehaving proxy or
// agent could produce.
func TestUploadReportRejects3xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	err := uploadReport(t.Context(), cfg, newTestReport())
	if err == nil {
		t.Fatal("expected error for 304 response, got nil")
	}
	if !strings.Contains(err.Error(), "304") {
		t.Errorf("error %q does not mention status code 304", err.Error())
	}
}

// TestUploadReportRetriesTransientFailure proves a crash report is no longer
// lost to a single transient failure: the intake returning 503 (Agent
// mid-restart) on the first attempt and 202 on the second must still result
// in a delivered report, not a permanently lost one.
func TestUploadReportRetriesTransientFailure(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	if err := uploadReport(t.Context(), cfg, newTestReport()); err != nil {
		t.Fatalf("uploadReport returned unexpected error: %v", err)
	}
	if requestCount != 2 {
		t.Errorf("requestCount = %d, want 2 (one failure, one success)", requestCount)
	}
}

// TestUploadReportReusesConnectionAcrossRetries proves uploadReport builds one
// client per call and drains each response body, so the three attempts share one
// TCP connection. A client per attempt, or an undrained body, opens a new one.
func TestUploadReportReusesConnectionAcrossRetries(t *testing.T) {
	var (
		mu          sync.Mutex
		connections int
		requests    int
	)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		if n < uploadAttempts {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("unavailable"))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			mu.Lock()
			connections++
			mu.Unlock()
		}
	}
	srv.Start()
	defer srv.Close()

	// No httpClient: this exercises the default client that uploadReport builds.
	cfg := &config{agentURL: srv.URL}

	if err := uploadReport(t.Context(), cfg, newTestReport()); err != nil {
		t.Fatalf("uploadReport returned unexpected error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != uploadAttempts {
		t.Fatalf("requests = %d, want %d", requests, uploadAttempts)
	}
	if connections != 1 {
		t.Errorf("connections = %d, want 1 (one client, drained bodies)", connections)
	}
}

// TestUploadReportRetries408 proves a 408 (the intake or an intervening
// proxy gave up waiting for the request) is treated as transient rather than
// a permanent 4xx: repeating the request unmodified is exactly what RFC 7231
// expects to resolve it, so giving up after one 408 would lose a report a
// retry could have delivered.
func TestUploadReportRetries408(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount == 1 {
			w.WriteHeader(http.StatusRequestTimeout)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	if err := uploadReport(t.Context(), cfg, newTestReport()); err != nil {
		t.Fatalf("uploadReport returned unexpected error: %v", err)
	}
	if requestCount != 2 {
		t.Errorf("requestCount = %d, want 2 (one 408, one success)", requestCount)
	}
}

// TestUploadReportDoesNotRetryBadRequest proves a non-retryable 4xx (which
// will fail identically on every attempt) does not waste the supervisor's
// remaining time before exit retrying a request that can never succeed.
func TestUploadReportDoesNotRetryBadRequest(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	err := uploadReport(t.Context(), cfg, newTestReport())
	if err == nil {
		t.Fatal("expected error for 400 response, got nil")
	}
	if requestCount != 1 {
		t.Errorf("requestCount = %d, want 1 (400 must not be retried)", requestCount)
	}
}

// TestUploadReportGivesUpAfterAllAttemptsFail proves uploadReport eventually
// stops rather than retrying forever, and that the final error is
// informative once every attempt has failed.
func TestUploadReportGivesUpAfterAllAttemptsFail(t *testing.T) {
	var requestCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := &config{
		agentURL:   srv.URL,
		httpClient: srv.Client(),
	}

	err := uploadReport(t.Context(), cfg, newTestReport())
	if err == nil {
		t.Fatal("expected error after every attempt fails, got nil")
	}
	if requestCount != uploadAttempts {
		t.Errorf("requestCount = %d, want %d", requestCount, uploadAttempts)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error %q does not mention the underlying status code 503", err.Error())
	}
}

// TestUploadReportRejectsRedirect proves the fix for the bug where a 3xx
// response was silently followed: net/http's default client converts a
// redirected POST to a GET and drops its body for 301/302/303, so if the
// redirect target answered 200, uploadReport would have reported success for
// a request that never actually carried the report. cfg deliberately omits
// httpClient (unlike every other test in this file) so this exercises
// uploadTarget.newClient's own internal.DefaultHTTPClient branch, the one the
// fix lives on and the one supervisor uploads use when no custom client is configured.
func TestUploadReportRejectsRedirect(t *testing.T) {
	var redirectRequests int
	mux := http.NewServeMux()
	mux.HandleFunc("/evp_proxy/v4/api/v2/errorsintake", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/redirected", http.StatusFound)
	})
	mux.HandleFunc("/redirected", func(w http.ResponseWriter, r *http.Request) {
		redirectRequests++
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := &config{agentURL: srv.URL}

	err := uploadReport(t.Context(), cfg, newTestReport())
	if err == nil {
		t.Fatal("expected error for a redirected response, got nil")
	}
	if !strings.Contains(err.Error(), "302") {
		t.Errorf("error %q does not mention the original 302 status", err.Error())
	}
	if redirectRequests != 0 {
		t.Error("client followed the redirect; want it rejected so a 3xx from the intake is never silently treated as success")
	}
}

// TestUploadReportUnixSocket proves the Unix socket agent path (URL scheme
// "unix://") actually reaches the socket: uploadTarget.newClient's UDSClient
// branch and the request's http://localhost URL rewrite need each other to
// work, and every other upload test uses a TCP httptest.Server so none of
// them previously exercised this branch at all.
func TestUploadReportUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		// filepath.Join(t.TempDir(), ...) on Windows produces a path with a
		// drive letter (e.g. C:\Users\...\agent.sock); "unix://" + that path
		// then fails net/url parsing, since the drive letter's colon looks
		// like a port specifier after the host. This is an artifact of how
		// this test builds its socket path, not a real production gap: a
		// unix:// agent URL is a POSIX concept a real Windows deployment
		// would not construct with a Windows-style path in the first place.
		t.Skip("unix domain socket agent URLs are not a realistic Windows configuration; see comment above")
	}
	socketPath := filepath.Join(t.TempDir(), "agent.sock")
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen on unix socket: %v", err)
	}

	var gotMethod, gotPath, gotSubdomain string
	var gotBody []byte
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotSubdomain = r.Header.Get("X-Datadog-EVP-Subdomain")
		gotBody = decompressBody(t, readAll(t, r.Body))
		w.WriteHeader(http.StatusAccepted)
	})}
	go srv.Serve(l)
	defer srv.Close()

	cfg := &config{agentURL: "unix://" + socketPath}

	if err := uploadReport(t.Context(), cfg, newTestReport()); err != nil {
		t.Fatalf("uploadReport over unix socket returned unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != agentEVPPath {
		t.Errorf("path = %q, want %q", gotPath, agentEVPPath)
	}
	if gotSubdomain != agentEVPSubdomain {
		t.Errorf("EVP subdomain = %q, want %q", gotSubdomain, agentEVPSubdomain)
	}
	assertRFC0013Body(t, gotBody)
}

// readAll reads r fully, failing the test on error.
func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return data
}

// TestBuildRequestToleratesDDSiteAsURL verifies the fix for DD_SITE supplied
// as a full URL ("https://datadoghq.com") rather than a bare host, which
// would otherwise interpolate into a malformed intake URL.
func TestBuildRequestToleratesDDSiteAsURL(t *testing.T) {
	cfg := &config{
		apiKey: "test-key",
		site:   "https://datadoghq.com/",
	}

	req, err := resolveUploadTarget(cfg).newRequest(cfg, []byte("{}"))
	if err != nil {
		t.Fatalf("newRequest returned unexpected error: %v", err)
	}

	wantHost := "error-tracking-intake.datadoghq.com"
	if req.URL.Host != wantHost {
		t.Errorf("target host = %q, want %q", req.URL.Host, wantHost)
	}
}
