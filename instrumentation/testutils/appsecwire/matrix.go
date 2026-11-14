// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package appsecwire

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	// BlockBody in [Want.Body] means the body of the default block response.
	BlockBody = "<block-body>"
	// AnyBody in [Want.Body] means that the body is not checked.
	AnyBody = "<any-body>"
)

// Want is what a request must produce.
type Want struct {
	// Status is the status that the client reads. 0 means no response.
	Status int
	// Body is the body that the client reads: an exact string, [BlockBody] or
	// [AnyBody].
	Body string
	// Location is the Location header that the client reads.
	Location string
	// Span is the http.status_code tag of every span.
	Span string
	// Blocked tells if a span has the appsec.blocked tag. With nested
	// handlers, only one span has it.
	Blocked bool
	// Error is the error flag of every span.
	Error bool
	// Outcome is the block outcome of the waf.requests metric (see [Result]).
	Outcome string
	// Logged tells if the net/http server logs a line.
	Logged bool
	// NotRun tells that the application handler must not run.
	NotRun bool
}

// Row is one case of the table.
type Row struct {
	Name    string
	Proto   Proto // the default is HTTP1
	Request Request
	// Handler is the application handler. Middlewares wrap the handler that
	// the contrib traces, the first is the outermost.
	Handler     http.Handler
	Middlewares []Middleware
	// Param tells that the request needs a route with a path parameter.
	Param bool
	Want  Want
}

// Contrib is what a contrib must give to run the table.
type Contrib struct {
	// Mount returns a handler that serves every request with h, with the
	// tracing of the contrib around h.
	Mount func(h http.Handler) http.Handler
	// MountParam is like Mount, but h serves only the route that has the path
	// parameter "id" (for example /users/{id}). It is nil if the contrib does
	// not match the route before the handler.
	MountParam func(h http.Handler) http.Handler
}

// Run sends the request of the row to the handler that c mounts, then checks
// the result against row.Want.
func (c Contrib) Run(t *testing.T, row Row) Result {
	t.Helper()
	ran := false
	app := row.Handler
	if row.Want.NotRun {
		app = Ran(t, app, &ran)
	}
	mount := c.Mount
	if row.Param {
		mount = c.MountParam
	}
	if mount == nil {
		t.Skip("the contrib does not support this row")
	}
	proto := row.Proto
	if proto == "" {
		proto = HTTP1
	}
	res := Serve(t, proto, Chain(mount(app), row.Middlewares...), row.Request)
	Check(t, res, row.Want)
	if row.Want.NotRun {
		assert.False(t, ran, "the application handler must not run")
	}
	return res
}

// Check compares the result with what is wanted.
func Check(t *testing.T, res Result, want Want) {
	t.Helper()
	assert.Equal(t, want.Status, res.Status, "status the client reads")
	switch want.Body {
	case AnyBody:
	case BlockBody:
		assert.True(t, IsBlockBody(res.Body), "body must be the block body, got %q", res.Body)
	default:
		assert.Equal(t, want.Body, res.Body, "body the client reads")
	}
	assert.Equal(t, want.Location, res.Location, "Location header")
	assert.Equal(t, want.Outcome, res.Outcome, "block outcome of waf.requests")
	assert.Equal(t, want.Logged, res.Log != "", "net/http log: %q", res.Log)
	if !assert.NotEmpty(t, res.Spans, "finished spans") {
		return
	}
	event, blocked := false, false
	for _, s := range res.Spans {
		assert.Equal(t, want.Span, s.Status, "http.status_code of the span")
		assert.Equal(t, want.Error, s.Error, "error flag of the span")
		event = event || s.Event
		blocked = blocked || s.Blocked
	}
	assert.Equal(t, want.Blocked, blocked, "appsec.blocked of the spans")
	assert.True(t, event, "_dd.appsec.json must be on a span")
}

// AcceptGzip is a request that accepts gzip. Without this header, gzhttp
// passes the Flush to the writer below.
var AcceptGzip = Request{Header: http.Header{"Accept-Encoding": {"gzip"}}}

var (
	// A block that reaches the client.
	blocked = Want{Status: http.StatusForbidden, Body: BlockBody, Span: "403", Blocked: true, Outcome: "blocked"}
)

// failed is a block that cannot reach the client: the application response
// started. The span shows what the client got.
func failed(status int, body, span string) Want {
	return Want{Status: status, Body: body, Span: span, Outcome: "failed"}
}

// CommitRows returns the rows that every contrib must pass. The handler sets
// X-Block: attack, then commits the response in different ways. Rows in
// [CommitRows] with the comment "fails on main" are the rows that the fix
// changes. The other rows pin the behavior that must not change.
func CommitRows() []Row {
	h := func(f http.HandlerFunc) http.Handler { return f }
	return []Row{
		// R1 (also P2): nothing is written. The block replaces the response.
		{Name: "R1-no-write", Handler: h(NoWrite), Want: blocked},
		// R2: a Write starts the response. No Flush.
		{Name: "R2-write-only", Handler: h(WriteOnly), Want: failed(200, AppBody, "200")},
		// R3: fails on main. Flush only. The status line is on the wire.
		{Name: "R3-flush-only", Handler: h(FlushOnly), Want: failed(200, "", "200")},
		// R4: fails on main.
		{Name: "R4-controller-flush", Handler: h(ControllerFlush), Want: failed(200, "", "200")},
		// R5: fails on main.
		{Name: "R5-flush-then-write", Handler: h(FlushThenWrite), Want: failed(200, AppBody, "200")},
		{Name: "R6-writeheader-then-flush", Handler: h(WriteHeaderThenFlush), Want: failed(200, "", "200")},
		{Name: "R7-write-then-flush", Handler: h(WriteThenFlush), Want: failed(200, AppBody, "200")},
		// R8: fails on main. The wrapper drops the WriteHeader(404), like net/http.
		{Name: "R8-flush-then-404", Handler: h(FlushThenNotFound), Want: failed(200, AppBody, "200")},
		// R9: fails on main. The span must not show the false 403.
		{Name: "R9-hijack-raw", Handler: h(HijackRaw), Want: failed(200, AppBody, "200")},
		// R10: fails on main.
		{Name: "R10-hijack-no-write", Handler: h(HijackNoWrite), Want: failed(0, "", "200")},
		// R11: fails on main.
		{Name: "R11-controller-hijack-no-write", Handler: h(ControllerHijackNoWrite), Want: failed(0, "", "200")},

		// G1: gzhttp ignores the Flush when the client accepts gzip. The 403
		// works on main and must work.
		{
			Name: "G1-gzip-flush-only", Handler: h(FlushOnly), Middlewares: []Middleware{Gzip},
			Request: AcceptGzip, Want: blocked,
		},
		// U3: a failed Hijack is not a commit.
		{Name: "U3-failed-hijack", Handler: h(FailedControllerHijack), Middlewares: []Middleware{FailedHijacker}, Want: blocked},

		// P1: request-phase block.
		{
			Name: "P1-request-header", Handler: h(WriteOnly),
			Request: Request{Header: http.Header{"X-Attack": {"attack"}}},
			Want:    Want{Status: 403, Body: BlockBody, Span: "403", Blocked: true, Outcome: "blocked", NotRun: true},
		},
		// P3: path-parameter block, after the router matched the route.
		{
			Name: "P3-path-param", Handler: h(WriteOnly), Param: true,
			Request: Request{Path: "/users/blocked-id"},
			Want:    Want{Status: 403, Body: BlockBody, Span: "403", Blocked: true, Outcome: "blocked", NotRun: true},
		},
		// P4: a blocked user before any write.
		{Name: "P4-set-user", Handler: h(SetUserBlocked), Want: blocked},
		// P6: response-phase redirect, no write.
		{
			Name: "P6-response-redirect", Handler: h(Redirect),
			Want: Want{Status: 303, Body: AnyBody, Location: "/blocked", Span: "303", Blocked: true, Outcome: "none"},
		},
		// P7: request-phase redirect.
		{
			Name: "P7-request-redirect", Handler: h(WriteOnly),
			Request: Request{Header: http.Header{"X-Redirect-Req": {"attack"}}},
			Want:    Want{Status: 303, Body: AnyBody, Location: "/blocked", Span: "303", Blocked: true, Outcome: "none", NotRun: true},
		},
	}
}

// RunMatrix runs [CommitRows] against the contrib.
func RunMatrix(t *testing.T, c Contrib) {
	Start(t)
	for _, row := range CommitRows() {
		t.Run(row.Name, func(t *testing.T) { c.Run(t, row) })
	}
}
