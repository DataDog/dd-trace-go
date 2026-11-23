// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fiber

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

// appSecHijackRules has 5 rules with different types, so that the WAF reports
// each match:
//   - the request header X-Attack: attack blocks (403) before the handler;
//   - the response header X-Block: attack blocks (403) after the handler;
//   - the response header X-Redirect: attack redirects (303) after the handler;
//   - the user ID attack-user blocks (403). The application sets it with the
//     SDK, and AppSec writes the block after the handler;
//   - the path parameter attack blocks (403). Only the router knows it, so the
//     block comes after the handler with Middleware, and before it with Wrap.
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
		"id": "hijack-path-param",
		"name": "Block path parameter",
		"tags": {"type": "test-hijack-path-param", "category": "attack_attempt"},
		"conditions": [{
			"operator": "exact_match",
			"parameters": {"inputs": [{"address": "server.request.path_params"}], "list": ["attack"]}
		}],
		"on_match": ["block"]
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

// hijackRawResponse is what a hijack handler of the application writes.
const hijackRawResponse = "HTTP/1.1 200 OK\r\nContent-Length: 8\r\nConnection: close\r\n\r\nAPP-BODY"

// hijackWait is the deadline of each read in this file. Reads that wait for the
// server to close the connection use readUntilClose. Reads of a keep-alive
// response use readFramed.
const hijackWait = 5 * time.Second

// hijackRawHandler is a hijack handler that writes a whole response, then
// closes the connection. ran tells that fasthttp called it.
func hijackRawHandler(ran *atomic.Bool) fasthttp.HijackHandler {
	return func(conn net.Conn) {
		ran.Store(true)
		_, _ = conn.Write([]byte(hijackRawResponse))
		_ = conn.Close()
	}
}

// hijackNoWriteHandler is a hijack handler that closes the connection without a
// write.
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

// waitConnState returns the last connection state after ServeConn returned.
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

// writeRequest writes one raw request. extra holds the extra header lines.
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

// parseFirstResponse returns the first response in raw, its body, and the
// bytes after it.
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

func startAppSecHijackRules(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.json")
	require.NoError(t, os.WriteFile(path, []byte(appSecHijackRules), 0o600))
	t.Setenv("DD_APPSEC_RULES", path)
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	testutils.StartAppSec(t)
}

// requireHijackSpan waits for the server span and returns it.
func requireHijackSpan(t *testing.T, mt mocktracer.Tracer) *mocktracer.Span {
	t.Helper()
	require.Eventually(t, func() bool { return len(mt.FinishedSpans()) == 1 }, hijackWait, time.Millisecond)
	return mt.FinishedSpans()[0]
}

// hijackRow is a handler case of the matrix.
type hijackRow struct {
	name string
	// closeRequest sends Connection: close in the request.
	closeRequest bool
	// handle is the application handler.
	handle func(c *fiber.Ctx, ran *atomic.Bool) error
}

func hijackRows() []hijackRow {
	raw := func(c *fiber.Ctx, ran *atomic.Bool) error {
		c.Context().Hijack(hijackRawHandler(ran))
		return nil
	}
	noResponseRaw := func(c *fiber.Ctx, ran *atomic.Bool) error {
		c.Context().HijackSetNoResponse(true)
		c.Context().Hijack(hijackRawHandler(ran))
		return nil
	}
	body := func(c *fiber.Ctx, _ *atomic.Bool) error { return c.SendString("APP-BODY") }
	return []hijackRow{
		{name: "hijack-raw", handle: raw},
		{name: "noresponse-raw", handle: noResponseRaw},
		{name: "hijack-nowrite", handle: func(c *fiber.Ctx, ran *atomic.Bool) error {
			c.Context().Hijack(hijackNoWriteHandler(ran))
			return nil
		}},
		{name: "hijack-raw-close", closeRequest: true, handle: raw},
		{name: "noresponse-raw-close", closeRequest: true, handle: noResponseRaw},
		{name: "noresponse-only", handle: func(c *fiber.Ctx, _ *atomic.Bool) error {
			c.Context().HijackSetNoResponse(true)
			return nil
		}},
		{name: "body", handle: body},
		// The hijack call comes from a middleware before Middleware.
		{name: "outer-hijack-before", handle: body},
		{name: "outer-hijack-after", handle: body},
	}
}

// hijackPhases are the block phases. none has no block. early-wrap uses Wrap
// and the path parameter. early-request uses Middleware and a request header.
// late-route uses Middleware and the path parameter, so the block comes after
// the handler.
var hijackPhases = []string{"none", "early-wrap", "early-request", "late-route", "response", "response-redirect", "user-sdk"}

func hijackApplicable(phase, row string) bool {
	switch row {
	case "outer-hijack-before":
		return phase == "early-request"
	case "outer-hijack-after":
		return phase == "response"
	}
	return true
}

func hijackEarly(phase string) bool { return phase == "early-wrap" || phase == "early-request" }

func hijackLate(phase string) bool {
	return phase == "late-route" || phase == "response" || phase == "response-redirect" || phase == "user-sdk"
}

// hijackServer builds the fiber app of a cell and returns its server.
func hijackServer(phase string, row hijackRow, ran *atomic.Bool) *fasthttp.Server {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	if phase == "early-wrap" {
		Wrap(app)
	} else {
		switch row.name {
		case "outer-hijack-before":
			app.Use(func(c *fiber.Ctx) error {
				c.Context().Hijack(hijackRawHandler(ran))
				return c.Next()
			})
		case "outer-hijack-after":
			app.Use(func(c *fiber.Ctx) error {
				err := c.Next()
				c.Context().Hijack(hijackRawHandler(ran))
				return err
			})
		}
		app.Use(Middleware())
	}
	app.Get("/benign", func(c *fiber.Ctx) error { return c.SendString("benign response") })
	app.Get("/p/:v", func(c *fiber.Ctx) error {
		switch phase {
		case "response":
			c.Set("X-Block", "attack")
		case "response-redirect":
			c.Set("X-Redirect", "attack")
		}
		err := row.handle(c, ran)
		if phase == "user-sdk" {
			// The application ignores the blocking error.
			_ = appsec.SetUser(c.UserContext(), "attack-user")
		}
		return err
	})
	// Build the route tree before serving.
	app.Handler()
	return app.Server()
}

// hijackTarget is the path of a cell.
func hijackTarget(phase string) string {
	if phase == "early-wrap" || phase == "late-route" {
		return "/p/attack"
	}
	return "/p/benign"
}

// requireBlockResponse checks one block response. The status is 303 with a
// Location for a redirect, and 403 with the block body otherwise.
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

// requireBlockTelemetry checks the span and the metric of a block that
// reached the client.
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

// TestAppSecHijackWire runs a real fasthttp server with a real WAF. Each cell
// is one application handler row in one block phase. After a block, the block
// response must be the last thing on the wire, and the hijack handler of the
// application must not run.
func TestAppSecHijackWire(t *testing.T) {
	startAppSecHijackRules(t)

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
				conn, done := servePipe(t, hijackServer(phase, row, &ran))
				br := bufio.NewReader(conn)

				var extra []string
				if row.closeRequest {
					extra = append(extra, "Connection: close")
				}
				if phase == "early-request" {
					extra = append(extra, "X-Attack: attack")
				}
				writeRequest(t, conn, hijackTarget(phase), extra...)

				if phase == "none" {
					requireNoBlockCell(t, row.name, conn, br, done, &ran)
					requireNoBlockTelemetry(t, mt, recorder.Metrics)
					return
				}
				switch {
				case row.name == "outer-hijack-after":
					// Code outside the AppSec scope hijacks after the block
					// (gap G1). Same result as before the fix.
					res, body, rest := parseFirstResponse(t, readUntilClose(t, conn))
					requireBlockResponse(t, phase, res, body)
					require.Equal(t, hijackRawResponse, rest)
					require.Equal(t, fasthttp.StateHijacked, waitConnState(t, done))
					require.True(t, ran.Load())
				case hijackEarly(phase) && row.name != "outer-hijack-before",
					hijackLate(phase) && (row.name == "body" || row.name == "noresponse-only"):
					if row.closeRequest {
						requireBlockThenClose(t, phase, conn, done, &ran)
						break
					}
					// The block response keeps the connection alive.
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

// requireBlockThenClose checks a block response with Connection: close, with
// no byte after it, and a hijack handler that did not run.
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

// hijackApp builds a fiber app with one route, and returns its server.
func hijackApp(handler fiber.Handler) *fasthttp.Server {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Use(Middleware())
	app.Get("/p/:v", handler)
	// Build the route tree before serving.
	app.Handler()
	return app.Server()
}

// TestAppSecHijackAfterApplicationTimeout makes the application call
// TimeoutErrorWithCode, then Hijack. fasthttp sends the timeout response from a
// new context, so the hijack call is lost and the block fails. The fix must
// not change this result.
func TestAppSecHijackAfterApplicationTimeout(t *testing.T) {
	startAppSecHijackRules(t)
	recorder := testutils.StartTelemetryRecorder(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	var ran atomic.Bool
	conn, done := servePipe(t, hijackApp(func(c *fiber.Ctx) error {
		c.Set("X-Block", "attack")
		c.Context().TimeoutErrorWithCode("application timeout", http.StatusGatewayTimeout)
		c.Context().HijackSetNoResponse(true)
		c.Context().Hijack(hijackRawHandler(&ran))
		return nil
	}))
	br := bufio.NewReader(conn)
	writeRequest(t, conn, "/p/benign")

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
	startAppSecHijackRules(t)
	recorder := testutils.StartTelemetryRecorder(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	var ran atomic.Bool
	start := make(chan struct{})
	finished := make(chan struct{})
	conn, done := servePipe(t, hijackApp(func(c *fiber.Ctx) error {
		c.Set("X-Block", "attack")
		fctx := c.Context()
		fctx.TimeoutErrorWithCode("application timeout", http.StatusGatewayTimeout)
		go func() {
			defer close(finished)
			<-start
			// Call both methods many times, so that the calls overlap with
			// the write of AppSec if the guard is missing.
			for range 100 {
				fctx.Hijack(hijackRawHandler(&ran))
				fctx.HijackSetNoResponse(true)
				runtime.Gosched()
			}
		}()
		close(start)
		return nil
	}))
	br := bufio.NewReader(conn)
	writeRequest(t, conn, "/p/benign")

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

// TestAppSecHijackResponseFlags checks the response flags after a block, on an
// in-memory context.
func TestAppSecHijackResponseFlags(t *testing.T) {
	startAppSecHijackRules(t)

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
			var ran atomic.Bool
			app := fiber.New(fiber.Config{DisableStartupMessage: true})
			app.Use(Middleware())
			app.Get("/", func(c *fiber.Ctx) error {
				if tc.block {
					c.Set("X-Block", "attack")
				}
				if tc.hijack {
					c.Context().Hijack(hijackRawHandler(&ran))
				}
				return nil
			})

			var req fasthttp.Request
			req.SetRequestURI("http://example.test/")
			var fctx fasthttp.RequestCtx
			fctx.Init(&req, &net.TCPAddr{}, nil)
			app.Handler()(&fctx)

			require.Equal(t, tc.wantClose, fctx.Response.ConnectionClose())
			require.Equal(t, tc.wantHijack, fctx.Hijacked())
			require.False(t, ran.Load(), "no test code runs a hijack handler")
			if tc.block {
				require.Equal(t, http.StatusForbidden, fctx.Response.StatusCode())
			}
		})
	}
}
