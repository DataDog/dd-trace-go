// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package appsecwire runs a real HTTP server, or a [httptest.ResponseRecorder],
// with the real WAF, and reports what the client reads, what the span holds and
// what the metrics hold. It exists to test what happens when the application
// commits the response (Flush or Hijack) before a response-phase AppSec block.
//
// The tests are black-box tests: they only use the public API of the contrib
// under test. One case table (see [CommitRows]) runs in each contrib that uses
// instrumentation/httptrace, so that a later wrapper in one contrib cannot
// bypass the fix without a failing test.
package appsecwire

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// Rules is the WAF rule set of the tests. All rules use a different type: with
// one type, the WAF does not report the second match.
//
//   - Response header X-Block: attack blocks (response phase).
//   - Request header X-Attack: attack blocks (request phase).
//   - Path parameter blocked-id blocks.
//   - User blocked-user blocks.
//   - Response header X-Redirect: attack redirects (303, /blocked).
//   - Request header X-Redirect-Req: attack redirects (303, /blocked).
const Rules = `{
	"version": "2.2",
	"metadata": {"rules_version": "1.0.0"},
	"actions": [{
		"id": "redirect",
		"type": "redirect_request",
		"parameters": {"status_code": 303, "location": "/blocked"}
	}],
	"rules": [{
		"id": "wire-response-header",
		"name": "Block response header",
		"tags": {"type": "test-response-header", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {
				"inputs": [{"address": "server.response.headers.no_cookies", "key_path": ["x-block"]}],
				"list": ["attack"]
			}
		}],
		"on_match": ["block"]
	}, {
		"id": "wire-request-header",
		"name": "Block request header",
		"tags": {"type": "test-request-header", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {
				"inputs": [{"address": "server.request.headers.no_cookies", "key_path": ["x-attack"]}],
				"list": ["attack"]
			}
		}],
		"on_match": ["block"]
	}, {
		"id": "wire-path-param",
		"name": "Block path parameter",
		"tags": {"type": "test-path-param", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {"inputs": [{"address": "server.request.path_params"}], "list": ["blocked-id"]}
		}],
		"on_match": ["block"]
	}, {
		"id": "wire-user",
		"name": "Block user",
		"tags": {"type": "test-user", "category": "security_response"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {"inputs": [{"address": "usr.id"}], "list": ["blocked-user"]}
		}],
		"on_match": ["block"]
	}, {
		"id": "wire-response-redirect",
		"name": "Redirect response header",
		"tags": {"type": "test-response-redirect", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {
				"inputs": [{"address": "server.response.headers.no_cookies", "key_path": ["x-redirect"]}],
				"list": ["attack"]
			}
		}],
		"on_match": ["redirect"]
	}, {
		"id": "wire-request-redirect",
		"name": "Redirect request header",
		"tags": {"type": "test-request-redirect", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {
				"inputs": [{"address": "server.request.headers.no_cookies", "key_path": ["x-redirect-req"]}],
				"list": ["attack"]
			}
		}],
		"on_match": ["redirect"]
	}]
}`

// Start writes [Rules] to a file, then starts AppSec with them. AppSec stops
// at the end of the test. The test is skipped if the platform has no WAF.
func Start(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.json")
	require.NoError(t, os.WriteFile(path, []byte(Rules), 0o600))
	t.Setenv("DD_APPSEC_RULES", path)
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	testutils.StartAppSec(t)
}

// Proto is the server (or recorder) that serves the request.
type Proto string

const (
	// HTTP1 is a real HTTP/1.1 server. The test reads the raw bytes of the
	// connection.
	HTTP1 Proto = "http1"
	// HTTP2 is a real TLS server that speaks HTTP/2.
	HTTP2 Proto = "http2"
	// H2C is a real server that speaks HTTP/2 without TLS (golang.org/x/net).
	H2C Proto = "h2c"
	// Recorder is a [httptest.ResponseRecorder]. There is no network.
	Recorder Proto = "recorder"
)

// Request is the request that the test sends.
type Request struct {
	// Path is the request path. The default is "/".
	Path string
	// Header holds the request headers.
	Header http.Header
}

// Span is what a finished span holds.
type Span struct {
	// Status is the http.status_code tag.
	Status string
	// Error tells if the span is marked as an error.
	Error bool
	// Blocked is the appsec.blocked tag.
	Blocked bool
	// Event tells if the span holds a security event (_dd.appsec.json).
	Event bool
}

// Result is what a request produced.
type Result struct {
	// Status is the status that the client read. It is 0 if the client read no
	// response.
	Status int
	// Body is the body that the client read.
	Body string
	// Location is the Location header that the client read.
	Location string
	// Wire holds the bytes of the connection (HTTP1 only).
	Wire string
	// Spans holds the finished spans.
	Spans []Span
	// Outcome is the block outcome of the waf.requests metric: "blocked"
	// (request_blocked:true), "failed" (block_failure:true), "none" (neither)
	// or "" (the metric is absent).
	Outcome string
	// Log holds what the net/http server logged (not for Recorder).
	Log string
}

// IsBlockBody tells if body is the body of the default block response.
func IsBlockBody(body string) bool {
	return strings.Contains(body, "You've been blocked")
}

// String shows the result on one line.
func (r Result) String() string {
	body := r.Body
	if IsBlockBody(body) {
		body = "<block-body>"
	}
	return fmt.Sprintf("status=%d body=%q location=%q spans=%+v outcome=%q log=%q", r.Status, body, r.Location, r.Spans, r.Outcome, r.Log)
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

const timeout = 5 * time.Second

// Serve sends req to h and reports the result. h must serve the request
// completely: Serve waits until h returns, then reads the spans and the
// metrics.
func Serve(t *testing.T, proto Proto, h http.Handler, req Request) Result {
	t.Helper()
	if req.Path == "" {
		req.Path = "/"
	}
	recorder := testutils.StartTelemetryRecorder(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	done := make(chan struct{})
	outer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		h.ServeHTTP(w, r)
	})

	var res Result
	logs := &syncBuffer{}
	if proto == Recorder {
		r := httptest.NewRequest(http.MethodGet, req.Path, nil)
		maps.Copy(r.Header, req.Header)
		rec := httptest.NewRecorder()
		outer.ServeHTTP(rec, r)
		fill(t, &res, rec.Result())
	} else {
		var handler http.Handler = outer
		if proto == H2C {
			handler = h2c.NewHandler(outer, &http2.Server{})
		}
		srv := httptest.NewUnstartedServer(handler)
		// slog.NewLogLogger gives the *log.Logger that http.Server needs. The
		// standard log package is not allowed in this file.
		srv.Config.ErrorLog = slog.NewLogLogger(slog.NewTextHandler(logs, &slog.HandlerOptions{
			ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey || a.Key == slog.LevelKey {
					return slog.Attr{} // keep the message only
				}
				return a
			},
		}), slog.LevelError)
		if proto == HTTP2 {
			srv.EnableHTTP2 = true
			srv.StartTLS()
		} else {
			srv.Start()
		}
		defer srv.Close()

		if proto == HTTP1 {
			res.Wire = rawHTTP1(t, srv.Listener.Addr().String(), req)
			parseWire(&res)
		} else {
			client := srv.Client()
			client.Timeout = timeout
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			if proto == H2C {
				p := new(http.Protocols)
				p.SetUnencryptedHTTP2(true)
				client.Transport = &http.Transport{Protocols: p}
			}
			r, err := http.NewRequest(http.MethodGet, srv.URL+req.Path, nil)
			require.NoError(t, err)
			maps.Copy(r.Header, req.Header)
			resp, err := client.Do(r)
			require.NoError(t, err)
			fill(t, &res, resp)
		}
	}
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("the handler did not return")
	}
	res.Log = logs.String()

	for _, s := range mt.FinishedSpans() {
		tags := s.Tags()
		status, _ := tags["http.status_code"].(string)
		res.Spans = append(res.Spans, Span{
			Status:  status,
			Error:   fmt.Sprint(s.Tag("_ddtrace.span_error")) == "1",
			Blocked: tags["appsec.blocked"] != nil,
			Event:   tags["_dd.appsec.json"] != nil,
		})
	}
	res.Outcome = outcome(recorder.Metrics)
	t.Logf("RESULT %s %s", t.Name(), res)
	return res
}

func fill(t *testing.T, res *Result, resp *http.Response) {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	res.Status = resp.StatusCode
	res.Body = string(body)
	res.Location = resp.Header.Get("Location")
}

// rawHTTP1 sends the request on a TCP connection and reads until the server
// closes the connection. A client library would hide the bytes after a Hijack.
func rawHTTP1(t *testing.T, addr string, req Request) string {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetDeadline(time.Now().Add(timeout)))
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\nHost: example.com\r\nConnection: close\r\n", req.Path)
	for k, vs := range req.Header {
		for _, v := range vs {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")
	_, err = conn.Write([]byte(b.String()))
	require.NoError(t, err)
	raw, _ := io.ReadAll(conn) // an error here means that the server reset the connection
	return string(raw)
}

func parseWire(res *Result) {
	br := bufio.NewReader(strings.NewReader(res.Wire))
	for {
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			return // no response on the wire
		}
		// Like a client, skip the informational responses (not 101).
		if resp.StatusCode >= 100 && resp.StatusCode < 200 && resp.StatusCode != http.StatusSwitchingProtocols {
			continue
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body) // a truncated body is reported as is
		res.Status = resp.StatusCode
		res.Body = string(body)
		res.Location = resp.Header.Get("Location")
		return
	}
}

// outcome returns the block outcome that the waf.requests metrics report.
func outcome(metrics map[telemetrytest.MetricKey]*telemetrytest.RecordMetricHandle) string {
	seen, blocked, failed := false, false, false
	for key, handle := range metrics {
		if handle.Get() <= 0 || key.Name != "waf.requests" {
			continue
		}
		seen = true
		for tag := range strings.SplitSeq(key.Tags, ",") {
			switch tag {
			case "request_blocked:true":
				blocked = true
			case "block_failure:true":
				failed = true
			}
		}
	}
	var out []string
	if blocked {
		out = append(out, "blocked")
	}
	if failed {
		out = append(out, "failed")
	}
	sort.Strings(out)
	switch {
	case len(out) > 0:
		return strings.Join(out, "+")
	case seen:
		return "none"
	}
	return ""
}
