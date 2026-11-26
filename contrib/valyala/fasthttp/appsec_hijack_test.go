// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fasthttp

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttpadaptor"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

const appSecHijackRules = `{
	"version": "2.2",
	"metadata": {"rules_version": "1.0.0"},
	"actions": [{
		"id": "redirect",
		"type": "redirect_request",
		"parameters": {"status_code": 303, "location": "/blocked"}
	}],
	"rules": [{
		"id": "hijack-request-header",
		"name": "Block request header",
		"tags": {"type": "test-hijack-request", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {
				"inputs": [{"address": "server.request.headers.no_cookies", "key_path": ["x-attack"]}],
				"list": ["attack"]
			}
		}],
		"on_match": ["block"]
	}, {
		"id": "hijack-response-header",
		"name": "Block response header",
		"tags": {"type": "test-hijack-response", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {
				"inputs": [{"address": "server.response.headers.no_cookies", "key_path": ["x-block"]}],
				"list": ["attack"]
			}
		}],
		"on_match": ["block"]
	}, {
		"id": "hijack-response-redirect",
		"name": "Redirect response header",
		"tags": {"type": "test-hijack-redirect", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {
				"inputs": [{"address": "server.response.headers.no_cookies", "key_path": ["x-redirect"]}],
				"list": ["attack"]
			}
		}],
		"on_match": ["redirect"]
	}, {
		"id": "hijack-user",
		"name": "Block user",
		"tags": {"type": "test-hijack-user", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {"inputs": [{"address": "usr.id"}], "list": ["attack-user"]}
		}],
		"on_match": ["block"]
	}]
}`

const hijackRawResponse = "HTTP/1.1 200 OK\r\nContent-Length: 8\r\nConnection: close\r\n\r\nAPP-BODY"

// hijackWait is the deadline of each read in this file. Reads that wait for the
// server to close the connection use readUntilClose. Reads of a keep-alive
// response use readFramed.
const hijackWait = 5 * time.Second

func hijackRawHandler(ran *atomic.Bool) fasthttp.HijackHandler {
	return func(conn net.Conn) {
		ran.Store(true)
		_, _ = conn.Write([]byte(hijackRawResponse))
		_ = conn.Close()
	}
}

func hijackNoWriteHandler(ran *atomic.Bool) fasthttp.HijackHandler {
	return func(conn net.Conn) {
		ran.Store(true)
		_ = conn.Close()
	}
}

// servePipe serves srv on one net.Pipe connection and returns the client side.
// The channel gets the last connection state after ServeConn returns.
// StateHijacked tells that fasthttp started the hijack handler. StateClosed
// tells that it did not.
func servePipe(t *testing.T, srv *fasthttp.Server) (net.Conn, <-chan fasthttp.ConnState) {
	t.Helper()
	var last atomic.Int32
	srv.ConnState = func(_ net.Conn, state fasthttp.ConnState) { last.Store(int32(state)) }
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })
	done := make(chan fasthttp.ConnState, 1)
	go func() {
		_ = srv.ServeConn(serverConn)
		done <- fasthttp.ConnState(last.Load())
	}()
	return clientConn, done
}

func waitConnState(t *testing.T, done <-chan fasthttp.ConnState) fasthttp.ConnState {
	t.Helper()
	select {
	case state := <-done:
		return state
	case <-time.After(hijackWait):
		t.Fatal("ServeConn did not return")
		return 0
	}
}

func writeRequest(t *testing.T, conn net.Conn, path string, extra ...string) {
	t.Helper()
	var req strings.Builder
	req.WriteString("GET " + path + " HTTP/1.1\r\nHost: example.test\r\n")
	for _, line := range extra {
		req.WriteString(line + "\r\n")
	}
	req.WriteString("\r\n")
	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(hijackWait)))
	_, err := conn.Write([]byte(req.String()))
	require.NoError(t, err)
}

// readUntilClose reads until the server closes the connection. Use it only
// when the server closes the connection: on a keep-alive connection, fasthttp
// keeps it open and the read waits until the deadline.
func readUntilClose(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(hijackWait)))
	raw, err := io.ReadAll(conn)
	require.NoError(t, err, "the server must close the connection")
	return raw
}

// wireResponse holds the parts of a response that the tests check. The body is
// already read and closed.
type wireResponse struct {
	StatusCode int
	Header     http.Header
	Close      bool
}

// readFramed reads one response and its whole body. Use it for each
// keep-alive response, with one reader for the whole connection.
func readFramed(t *testing.T, conn net.Conn, br *bufio.Reader) (wireResponse, string) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(hijackWait)))
	res, err := http.ReadResponse(br, nil)
	require.NoError(t, err)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	return wireResponse{StatusCode: res.StatusCode, Header: res.Header, Close: res.Close}, string(body)
}

func parseFirstResponse(t *testing.T, raw []byte) (wireResponse, string, string) {
	t.Helper()
	br := bufio.NewReader(bytes.NewReader(raw))
	res, err := http.ReadResponse(br, nil)
	require.NoError(t, err, "wire: %q", raw)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	rest, err := io.ReadAll(br)
	require.NoError(t, err)
	return wireResponse{StatusCode: res.StatusCode, Header: res.Header, Close: res.Close}, string(body), string(rest)
}

// requireWAFOutcome checks the block outcome that the waf.requests metric
// reports. The telemetry recorder type is internal to dd-trace-go/v2, so this
// function accepts its metrics map through type inference. AppSec submits the
// metric before the response goes out, so one read is enough.
func requireWAFOutcome[K comparable, H interface{ Get() float64 }](t *testing.T, metrics map[K]H, blocked, failed bool) {
	t.Helper()
	var outcomes []string
	for key, handle := range metrics {
		if handle.Get() <= 0 {
			continue
		}
		v := reflect.ValueOf(key)
		if v.FieldByName("Name").String() != "waf.requests" {
			continue
		}
		for tag := range strings.SplitSeq(v.FieldByName("Tags").String(), ",") {
			if strings.HasPrefix(tag, "request_blocked:") || strings.HasPrefix(tag, "block_failure:") {
				outcomes = append(outcomes, tag)
			}
		}
	}
	require.ElementsMatch(t, []string{"request_blocked:" + strconv.FormatBool(blocked), "block_failure:" + strconv.FormatBool(failed)}, outcomes)
}

func requireHijackSpan(t *testing.T, mt mocktracer.Tracer) *mocktracer.Span {
	t.Helper()
	// servePipe calls Server.ServeConn, which Orchestrion does not instrument.
	// There is one server span, with or without Orchestrion.
	require.Eventually(t, func() bool { return len(timeoutServerSpans(mt)) == 1 }, hijackWait, time.Millisecond)
	return timeoutServerSpans(mt)[0]
}

type hijackRow struct {
	name         string
	closeRequest bool
	handle       func(ctx *fasthttp.RequestCtx, ran *atomic.Bool)
}

func hijackRows() []hijackRow {
	raw := func(ctx *fasthttp.RequestCtx, ran *atomic.Bool) { ctx.Hijack(hijackRawHandler(ran)) }
	noResponseRaw := func(ctx *fasthttp.RequestCtx, ran *atomic.Bool) {
		ctx.HijackSetNoResponse(true)
		ctx.Hijack(hijackRawHandler(ran))
	}
	return []hijackRow{
		{name: "hijack-raw", handle: raw},
		{name: "noresponse-raw", handle: noResponseRaw},
		{name: "hijack-nowrite", handle: func(ctx *fasthttp.RequestCtx, ran *atomic.Bool) { ctx.Hijack(hijackNoWriteHandler(ran)) }},
		{name: "hijack-raw-close", closeRequest: true, handle: raw},
		{name: "noresponse-raw-close", closeRequest: true, handle: noResponseRaw},
		{name: "noresponse-only", handle: func(ctx *fasthttp.RequestCtx, _ *atomic.Bool) { ctx.HijackSetNoResponse(true) }},
		{name: "body", handle: func(ctx *fasthttp.RequestCtx, _ *atomic.Bool) { ctx.SetBodyString("APP-BODY") }},
		// The hijack call comes from code outside the AppSec scope.
		{name: "outer-hijack-before", handle: func(ctx *fasthttp.RequestCtx, _ *atomic.Bool) { ctx.SetBodyString("APP-BODY") }},
		{name: "outer-hijack-after", handle: func(ctx *fasthttp.RequestCtx, _ *atomic.Bool) { ctx.SetBodyString("APP-BODY") }},
	}
}

var hijackPhases = []string{"none", "early-request", "response", "response-redirect", "user-sdk"}

func hijackApplicable(phase, row string) bool {
	switch row {
	case "outer-hijack-before":
		return phase == "early-request"
	case "outer-hijack-after":
		return phase == "response"
	}
	return true
}

func hijackLate(phase string) bool {
	return phase == "response" || phase == "response-redirect" || phase == "user-sdk"
}

// hijackHandlerFor builds the server handler of a cell. /benign is answered
// without AppSec, for the second request of a keep-alive check.
func hijackHandlerFor(phase string, row hijackRow, ran *atomic.Bool) fasthttp.RequestHandler {
	wrapped := WrapHandler(func(ctx *fasthttp.RequestCtx) {
		switch phase {
		case "response":
			ctx.Response.Header.Set("X-Block", "attack")
		case "response-redirect":
			ctx.Response.Header.Set("X-Redirect", "attack")
		}
		row.handle(ctx, ran)
		if phase == "user-sdk" {
			// The application ignores the blocking error.
			_ = appsec.SetUser(ctx, "attack-user")
		}
	})
	switch row.name {
	case "outer-hijack-before":
		inner := wrapped
		wrapped = func(ctx *fasthttp.RequestCtx) {
			ctx.Hijack(hijackRawHandler(ran))
			inner(ctx)
		}
	case "outer-hijack-after":
		inner := wrapped
		wrapped = func(ctx *fasthttp.RequestCtx) {
			inner(ctx)
			ctx.Hijack(hijackRawHandler(ran))
		}
	}
	return func(ctx *fasthttp.RequestCtx) {
		if string(ctx.Path()) == "/benign" {
			ctx.SetBodyString("benign response")
			return
		}
		wrapped(ctx)
	}
}

func requireBlockResponse(t *testing.T, phase string, res wireResponse, body string) {
	t.Helper()
	if phase == "response-redirect" {
		require.Equal(t, http.StatusSeeOther, res.StatusCode)
		require.Equal(t, "/blocked", res.Header.Get("Location"))
		require.NotContains(t, body, "APP-BODY")
		return
	}
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Contains(t, body, "You've been blocked")
	require.NotContains(t, body, "APP-BODY")
}

func requireBlockTelemetry[K comparable, H interface{ Get() float64 }](t *testing.T, phase string, mt mocktracer.Tracer, metrics map[K]H) {
	t.Helper()
	span := requireHijackSpan(t, mt)
	require.Equal(t, "true", span.Tag("appsec.blocked"))
	if phase == "response-redirect" {
		// A redirect is an applied action, but not a block.
		require.Equal(t, "303", span.Tag("http.status_code"))
		requireWAFOutcome(t, metrics, false, false)
		return
	}
	require.Equal(t, "403", span.Tag("http.status_code"))
	requireWAFOutcome(t, metrics, true, false)
}

type hijackContextCloser struct {
	closed atomic.Bool
}

func (c *hijackContextCloser) Close() error {
	c.closed.Store(true)
	return nil
}

func TestAppSecHijackAdaptorKeepsContext(t *testing.T) {
	startAppSecRules(t, appSecHijackRules)
	mt := mocktracer.Start()
	defer mt.Stop()

	const key = "adaptor-context-value"
	value := &hijackContextCloser{}
	started := make(chan struct{})
	finished := make(chan struct{})
	check := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		select {
		case <-started:
			select {
			case <-finished:
			case <-time.After(hijackWait):
				t.Error("adapted handler did not finish")
			}
		default:
		}
	})
	hijacked := make(chan error, 1)
	observed := make(chan any, 1)
	adapted := fasthttpadaptor.NewFastHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		defer close(finished)
		conn, _, err := w.(http.Hijacker).Hijack()
		hijacked <- err
		if err != nil {
			return
		}
		defer conn.Close()
		select {
		case <-check:
			observed <- r.Context().Value(key)
		case <-release:
			return
		}
		<-release
	}))
	wrapped := WrapHandler(func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("X-Block", "attack")
		adapted(ctx)
	})
	srv := &fasthttp.Server{Handler: func(ctx *fasthttp.RequestCtx) {
		ctx.SetUserValue(key, value)
		wrapped(ctx)
	}}
	conn, done := servePipe(t, srv)
	writeRequest(t, conn, "/case")
	res, body, rest := parseFirstResponse(t, readUntilClose(t, conn))
	requireBlockResponse(t, "response", res, body)
	require.Empty(t, rest)
	require.Equal(t, fasthttp.StateClosed, waitConnState(t, done))
	select {
	case err := <-hijacked:
		require.NoError(t, err)
	case <-time.After(hijackWait):
		t.Fatal("adapted handler did not hijack")
	}
	close(check)
	select {
	case got := <-observed:
		require.Same(t, value, got)
	case <-time.After(hijackWait):
		t.Fatal("adapted handler did not read its context")
	}
	require.False(t, value.closed.Load())
	select {
	case <-finished:
		t.Fatal("adapted handler returned before release")
	default:
	}
}

// TestAppSecHijackWire runs a real fasthttp server with a real WAF. Each cell
// is one application handler row in one block phase. After a block, the block
// response must be the last thing on the wire, and the hijack handler of the
// application must not run.
func TestAppSecHijackWire(t *testing.T) {
	startAppSecRules(t, appSecHijackRules)

	for _, phase := range hijackPhases {
		for _, row := range hijackRows() {
			if !hijackApplicable(phase, row.name) {
				continue
			}
			t.Run(phase+"/"+row.name, func(t *testing.T) {
				recorder := testutils.StartTelemetryRecorder(t)
				mt := mocktracer.Start()
				defer mt.Stop()

				var ran atomic.Bool
				srv := &fasthttp.Server{Handler: hijackHandlerFor(phase, row, &ran)}
				conn, done := servePipe(t, srv)
				br := bufio.NewReader(conn)

				var extra []string
				if row.closeRequest {
					extra = append(extra, "Connection: close")
				}
				if phase == "early-request" {
					extra = append(extra, "X-Attack: attack")
				}
				writeRequest(t, conn, "/case", extra...)

				if phase == "none" {
					requireNoBlockCell(t, row.name, conn, br, done, &ran)
					requireNoBlockTelemetry(t, mt, recorder.Metrics)
					return
				}
				switch {
				case row.name == "outer-hijack-after":
					res, body, rest := parseFirstResponse(t, readUntilClose(t, conn))
					requireBlockResponse(t, phase, res, body)
					require.Equal(t, hijackRawResponse, rest)
					require.Equal(t, fasthttp.StateHijacked, waitConnState(t, done))
					require.True(t, ran.Load())
				case phase == "early-request" && row.name != "outer-hijack-before",
					hijackLate(phase) && (row.name == "body" || row.name == "noresponse-only"):
					if row.closeRequest {
						requireBlockThenClose(t, phase, conn, done, &ran)
						break
					}
					res, body := readFramed(t, conn, br)
					requireBlockResponse(t, phase, res, body)
					require.False(t, res.Close)
					requireBlockTelemetry(t, phase, mt, recorder.Metrics)
					writeRequest(t, conn, "/benign")
					res, body = readFramed(t, conn, br)
					require.Equal(t, http.StatusOK, res.StatusCode)
					require.Equal(t, "benign response", body)
					require.NoError(t, conn.Close())
					require.Equal(t, fasthttp.StateClosed, waitConnState(t, done))
					require.False(t, ran.Load())
					return
				default:
					requireBlockThenClose(t, phase, conn, done, &ran)
				}
				requireBlockTelemetry(t, phase, mt, recorder.Metrics)
			})
		}
	}
}

func requireBlockThenClose(t *testing.T, phase string, conn net.Conn, done <-chan fasthttp.ConnState, ran *atomic.Bool) {
	t.Helper()
	res, body, rest := parseFirstResponse(t, readUntilClose(t, conn))
	requireBlockResponse(t, phase, res, body)
	require.True(t, res.Close, "the block response must close the connection")
	require.Empty(t, rest, "no byte may follow the block response")
	require.Equal(t, fasthttp.StateClosed, waitConnState(t, done), "fasthttp must not start the hijack handler")
	require.False(t, ran.Load())
}

// requireNoBlockCell checks what the application alone sends. These cells show
// that the other cells change only because of the block.
func requireNoBlockCell(t *testing.T, row string, conn net.Conn, br *bufio.Reader, done <-chan fasthttp.ConnState, ran *atomic.Bool) {
	t.Helper()
	switch row {
	case "noresponse-only", "body":
		res, body := readFramed(t, conn, br)
		require.Equal(t, http.StatusOK, res.StatusCode)
		if row == "body" {
			require.Equal(t, "APP-BODY", body)
		} else {
			require.Empty(t, body)
		}
		require.NoError(t, conn.Close())
		require.Equal(t, fasthttp.StateClosed, waitConnState(t, done))
		require.False(t, ran.Load())
	case "noresponse-raw", "noresponse-raw-close":
		// fasthttp sends no response of its own. Only the application writes.
		require.Equal(t, hijackRawResponse, string(readUntilClose(t, conn)))
		require.Equal(t, fasthttp.StateHijacked, waitConnState(t, done))
		require.True(t, ran.Load())
	case "hijack-raw", "hijack-nowrite":
		res, body, rest := parseFirstResponse(t, readUntilClose(t, conn))
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Empty(t, body)
		if row == "hijack-raw" {
			require.Equal(t, hijackRawResponse, rest)
		} else {
			require.Empty(t, rest)
		}
		require.Equal(t, fasthttp.StateHijacked, waitConnState(t, done))
		require.True(t, ran.Load())
	case "hijack-raw-close":
		// fasthttp does not call a hijack handler on a request with
		// Connection: close.
		res, body, rest := parseFirstResponse(t, readUntilClose(t, conn))
		require.Equal(t, http.StatusOK, res.StatusCode)
		require.True(t, res.Close)
		require.Empty(t, body)
		require.Empty(t, rest)
		require.Equal(t, fasthttp.StateClosed, waitConnState(t, done))
		require.False(t, ran.Load())
	default:
		t.Fatalf("unknown row %q", row)
	}
}

func requireNoBlockTelemetry[K comparable, H interface{ Get() float64 }](t *testing.T, mt mocktracer.Tracer, metrics map[K]H) {
	t.Helper()
	span := requireHijackSpan(t, mt)
	require.Nil(t, span.Tag("appsec.blocked"))
	requireWAFOutcome(t, metrics, false, false)
}

// TestAppSecHijackAfterApplicationTimeout makes the application call
// TimeoutErrorWithCode, then Hijack. fasthttp sends the timeout response from a
// new context, so the hijack call is lost and the block fails. The fix must
// not change this result.
func TestAppSecHijackAfterApplicationTimeout(t *testing.T) {
	startAppSecRules(t, appSecHijackRules)
	recorder := testutils.StartTelemetryRecorder(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	var ran atomic.Bool
	srv := &fasthttp.Server{Handler: WrapHandler(func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("X-Block", "attack")
		ctx.TimeoutErrorWithCode("application timeout", http.StatusGatewayTimeout)
		ctx.HijackSetNoResponse(true)
		ctx.Hijack(hijackRawHandler(&ran))
	})}
	conn, done := servePipe(t, srv)
	br := bufio.NewReader(conn)
	writeRequest(t, conn, "/case")

	res, body := readFramed(t, conn, br)
	require.Equal(t, http.StatusGatewayTimeout, res.StatusCode)
	require.Equal(t, "application timeout", body)
	span := requireHijackSpan(t, mt)
	require.Nil(t, span.Tag("appsec.blocked"))
	requireWAFOutcome(t, recorder.Metrics, false, true)
	require.NoError(t, conn.Close())
	require.Equal(t, fasthttp.StateClosed, waitConnState(t, done))
	require.False(t, ran.Load())
}

// TestAppSecHijackConcurrentAfterApplicationTimeout makes another goroutine
// call Hijack and HijackSetNoResponse on the context after
// TimeoutErrorWithCode. The block fails, and AppSec must not write to the
// context: the other goroutine can still use it. Run it with -race.
func TestAppSecHijackConcurrentAfterApplicationTimeout(t *testing.T) {
	startAppSecRules(t, appSecHijackRules)
	recorder := testutils.StartTelemetryRecorder(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	var ran atomic.Bool
	start := make(chan struct{})
	finished := make(chan struct{})
	srv := &fasthttp.Server{Handler: WrapHandler(func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("X-Block", "attack")
		ctx.TimeoutErrorWithCode("application timeout", http.StatusGatewayTimeout)
		go func() {
			defer close(finished)
			<-start
			// Call both methods many times, so that the calls overlap with
			// the write of AppSec if the guard is missing.
			for range 100 {
				ctx.Hijack(hijackRawHandler(&ran))
				ctx.HijackSetNoResponse(true)
				runtime.Gosched()
			}
		}()
		close(start)
	})}
	conn, done := servePipe(t, srv)
	br := bufio.NewReader(conn)
	writeRequest(t, conn, "/case")

	res, body := readFramed(t, conn, br)
	require.Equal(t, http.StatusGatewayTimeout, res.StatusCode)
	require.Equal(t, "application timeout", body)
	span := requireHijackSpan(t, mt)
	require.Nil(t, span.Tag("appsec.blocked"))
	requireWAFOutcome(t, recorder.Metrics, false, true)
	select {
	case <-finished:
	case <-time.After(hijackWait):
		t.Fatal("the goroutine of the application did not finish")
	}
	require.NoError(t, conn.Close())
	require.Equal(t, fasthttp.StateClosed, waitConnState(t, done))
	require.False(t, ran.Load())
}

func hijackTimeoutOrders(app fasthttp.RequestHandler) map[string]fasthttp.RequestHandler {
	return map[string]fasthttp.RequestHandler{
		"wrap-inside":  TimeoutHandler(WrapHandler(app), time.Hour, "outer"),
		"wrap-outside": WrapHandler(TimeoutHandler(app, time.Hour, "outer")),
	}
}

// TestTimeoutHandlerHijackAtDeadline blocks a user at the deadline of a nested
// timeout wrapper. AppSec writes into the detached timeout response while
// the worker can still use the live context, so AppSec must not write to it.
// The worker calls Hijack once or repeatedly across the deadline.
func TestTimeoutHandlerHijackAtDeadline(t *testing.T) {
	startAppSecRules(t, appSecHijackRules)

	for _, loop := range []bool{false, true} {
		for _, order := range []string{"wrap-inside", "wrap-outside"} {
			name := "once"
			if loop {
				name = "loop"
			}
			t.Run(name+"/"+order, func(t *testing.T) {
				recorder := testutils.StartTelemetryRecorder(t)
				mt := mocktracer.Start()
				defer mt.Stop()

				var ran atomic.Bool
				release := make(chan struct{})
				var once sync.Once
				t.Cleanup(func() { once.Do(func() { close(release) }) })
				exited := make(chan (<-chan struct{}), 1)
				app := func(ctx *fasthttp.RequestCtx) {
					exited <- ctx.UserValue(timeoutContextKey{}).(*timeoutLayer).workerExited
					// The block is pending before the nested deadline starts.
					_ = appsec.SetUser(ctx, "attack-user")
					if !loop {
						ctx.Hijack(hijackRawHandler(&ran))
						ctx.HijackSetNoResponse(true)
						nestedTimeoutHandler(release)(ctx)
						return
					}
					// The worker never waits for the client: a wait makes a
					// happens-before edge, and -race would not see a write of
					// AppSec to the live context.
					TimeoutWithCodeHandler(func(ctx *fasthttp.RequestCtx) {
						for {
							ctx.Hijack(hijackRawHandler(&ran))
							ctx.HijackSetNoResponse(true)
							select {
							case <-release:
								return
							default:
								runtime.Gosched()
							}
						}
					}, 20*time.Millisecond, "timeout", http.StatusInternalServerError)(ctx)
				}
				srv := &fasthttp.Server{Handler: hijackTimeoutOrders(app)[order]}
				conn, done := servePipe(t, srv)
				br := bufio.NewReader(conn)
				writeRequest(t, conn, "/case")

				res, body := readFramed(t, conn, br)
				requireBlockResponse(t, "user-sdk", res, body)
				requireBlockTelemetry(t, "user-sdk", mt, recorder.Metrics)
				once.Do(func() { close(release) })
				require.NoError(t, conn.Close())
				require.Equal(t, fasthttp.StateClosed, waitConnState(t, done))
				waitTimeoutWorker(t, <-exited)
				require.False(t, ran.Load())
			})
		}
	}
}

// TestTimeoutHandlerHijackBeforeDeadline blocks the response of a request with
// a hijack handler, with a long deadline. The block goes into the live
// response, so it must close the connection and stop the hijack handler.
func TestTimeoutHandlerHijackBeforeDeadline(t *testing.T) {
	startAppSecRules(t, appSecHijackRules)

	for _, order := range []string{"wrap-inside", "wrap-outside"} {
		t.Run(order, func(t *testing.T) {
			recorder := testutils.StartTelemetryRecorder(t)
			mt := mocktracer.Start()
			defer mt.Stop()

			var ran atomic.Bool
			app := func(ctx *fasthttp.RequestCtx) {
				ctx.Response.Header.Set("X-Block", "attack")
				ctx.Hijack(hijackRawHandler(&ran))
			}
			srv := &fasthttp.Server{Handler: hijackTimeoutOrders(app)[order]}
			conn, done := servePipe(t, srv)
			writeRequest(t, conn, "/case")

			requireBlockThenClose(t, "response", conn, done, &ran)
			requireBlockTelemetry(t, "response", mt, recorder.Metrics)
		})
	}
}

func TestAppSecHijackResponseFlags(t *testing.T) {
	startAppSecRules(t, appSecHijackRules)

	for _, tc := range []struct {
		name       string
		block      bool
		hijack     bool
		wantClose  bool
		wantHijack bool
	}{
		{name: "block-after-hijack", block: true, hijack: true, wantClose: true, wantHijack: true},
		{name: "block-without-hijack", block: true},
		{name: "no-block-after-hijack", hijack: true, wantHijack: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var req fasthttp.Request
			req.SetRequestURI("http://example.test/")
			var fctx fasthttp.RequestCtx
			fctx.Init(&req, &net.TCPAddr{}, nil)

			var ran atomic.Bool
			WrapHandler(func(ctx *fasthttp.RequestCtx) {
				if tc.block {
					ctx.Response.Header.Set("X-Block", "attack")
				}
				if tc.hijack {
					ctx.Hijack(hijackRawHandler(&ran))
				}
			})(&fctx)

			require.Equal(t, tc.wantClose, fctx.Response.ConnectionClose())
			require.Equal(t, tc.wantHijack, fctx.Hijacked())
			require.False(t, ran.Load(), "no test code runs a hijack handler")
			if tc.block {
				require.Equal(t, http.StatusForbidden, fctx.Response.StatusCode())
			}
		})
	}
}
