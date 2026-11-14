// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package http

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/appsecwire"
)

func appSecCommitContrib() appsecwire.Contrib {
	return appsecwire.Contrib{
		Mount: func(h http.Handler) http.Handler { return WrapHandler(h, "service", "resource") },
		MountParam: func(h http.Handler) http.Handler {
			mux := NewServeMux()
			mux.Handle("/users/{id}", h)
			return mux
		},
	}
}

// TestAppSecResponseCommit runs the case table of appsecwire. It uses a real
// server and the real WAF.
func TestAppSecResponseCommit(t *testing.T) {
	appsecwire.RunMatrix(t, appSecCommitContrib())
}

// TestAppSecResponseCommitWriters covers the rows that test the writer under
// httptrace, not the contrib. The first part has a known server writer under
// httptrace, so that the fix applies. The second part has another writer under
// httptrace, so that the behavior of main must stay.
func TestAppSecResponseCommitWriters(t *testing.T) {
	appsecwire.Start(t)
	c := appSecCommitContrib()
	flushed := appsecwire.Want{Status: 200, Span: "200", Outcome: "failed"}

	run := func(t *testing.T, proto appsecwire.Proto, h http.HandlerFunc, want appsecwire.Want, mws ...appsecwire.Middleware) appsecwire.Result {
		t.Helper()
		return c.Run(t, appsecwire.Row{Name: t.Name(), Proto: proto, Handler: h, Middlewares: mws, Want: want})
	}

	// A. Known server writer under httptrace: the fix applies.
	t.Run("R12-http2-flush-only", func(t *testing.T) {
		run(t, appsecwire.HTTP2, appsecwire.FlushOnly, flushed)
	})
	t.Run("R13-nested-flush-only", func(t *testing.T) {
		nested := appsecwire.Contrib{Mount: func(h http.Handler) http.Handler {
			return WrapHandler(WrapHandler(h, "inner", "inner"), "outer", "outer")
		}}
		nested.Run(t, appsecwire.Row{Handler: http.HandlerFunc(appsecwire.FlushOnly), Want: flushed})
	})
	t.Run("R15-recorder-flush-only", func(t *testing.T) {
		run(t, appsecwire.Recorder, appsecwire.FlushOnly, flushed)
	})
	t.Run("R16-h2c-flush-only", func(t *testing.T) {
		run(t, appsecwire.H2C, appsecwire.FlushOnly, flushed)
	})
	// A Flush after an informational response is still the start of the final
	// response. The recorder locks the status 103.
	t.Run("R17-early-hints-http1", func(t *testing.T) {
		run(t, appsecwire.HTTP1, appsecwire.FlushOnly, flushed, appsecwire.EarlyHints)
	})
	t.Run("R17-early-hints-http2", func(t *testing.T) {
		run(t, appsecwire.HTTP2, appsecwire.FlushOnly, flushed, appsecwire.EarlyHints)
	})
	t.Run("R17-early-hints-recorder", func(t *testing.T) {
		want := flushed
		want.Status = http.StatusEarlyHints
		run(t, appsecwire.Recorder, appsecwire.FlushOnly, want, appsecwire.EarlyHints)
	})

	// B. Another writer under httptrace: the behavior of main stays.
	blocked := appsecwire.Want{Status: 403, Body: appsecwire.BlockBody, Span: "403", Blocked: true, Outcome: "blocked"}
	t.Run("G2-gzip-around-nested-flush-only", func(t *testing.T) {
		nested := appsecwire.Contrib{Mount: func(h http.Handler) http.Handler {
			return WrapHandler(WrapHandler(h, "inner", "inner"), "outer", "outer")
		}}
		nested.Run(t, appsecwire.Row{
			Handler: http.HandlerFunc(appsecwire.FlushOnly), Middlewares: []appsecwire.Middleware{appsecwire.Gzip},
			Request: appsecwire.AcceptGzip, Want: blocked,
		})
	})
	// Known gap (decision a): a pass-through wrapper forwards the Flush, but
	// httptrace cannot know that the wrapper really sends it.
	t.Run("G4-pass-through-flush-only", func(t *testing.T) {
		want := appsecwire.Want{Status: 200, Body: appsecwire.BlockBody, Span: "403", Blocked: true, Outcome: "blocked", Logged: true}
		run(t, appsecwire.HTTP1, appsecwire.FlushOnly, want, appsecwire.PassThrough)
	})
	t.Run("G5-ignored-flush", func(t *testing.T) {
		run(t, appsecwire.HTTP1, appsecwire.FlushOnly, blocked, appsecwire.IgnoredFlush)
	})
	t.Run("G6-panic-flush", func(t *testing.T) {
		recovered := func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = recover() }()
			appsecwire.FlushOnly(w, r)
		}
		run(t, appsecwire.HTTP1, recovered, blocked, appsecwire.PanicFlush)
	})
	t.Run("G7-same-name-recorder", func(t *testing.T) {
		run(t, appsecwire.HTTP1, appsecwire.FlushOnly, blocked, appsecwire.SameNameRecorder)
	})
	// Known gap (decision e): a writer above httptrace that has only Unwrap lets
	// a ResponseController go around httptrace to the raw writer.
	t.Run("U1-unwrap-only-controller-flush", func(t *testing.T) {
		want := appsecwire.Want{Status: 200, Body: appsecwire.BlockBody, Span: "403", Blocked: true, Outcome: "blocked", Logged: true}
		run(t, appsecwire.HTTP1, appsecwire.ControllerFlush, want, appsecwire.UnwrapOnly)
	})
	t.Run("U2-unwrap-only-controller-hijack", func(t *testing.T) {
		want := appsecwire.Want{Status: 200, Body: appsecwire.AppBody, Span: "403", Blocked: true, Outcome: "blocked", Logged: true}
		run(t, appsecwire.HTTP1, appsecwire.ControllerHijackRaw, want, appsecwire.UnwrapOnly)
	})
}

// TestAppSecResponseCommitWithoutAppSec checks the effect of the fix when
// AppSec is off.
func TestAppSecResponseCommitWithoutAppSec(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "false")
	mount := func(h http.Handler) http.Handler { return WrapHandler(h, "service", "resource") }

	// R14: net/http logs "superfluous response.WriteHeader" if the status 0
	// stays after a Flush. A known flush records the status 200.
	t.Run("R14-flush-then-write", func(t *testing.T) {
		res := appsecwire.Serve(t, appsecwire.HTTP1, mount(http.HandlerFunc(appsecwire.FlushThenWrite)), appsecwire.Request{})
		assert.Equal(t, 200, res.Status)
		assert.Equal(t, appsecwire.AppBody, res.Body)
		assert.Empty(t, res.Log)
		require.Len(t, res.Spans, 1)
		assert.Equal(t, "200", res.Spans[0].Status)
	})

	// G3: gzhttp ignores the Flush, so the application status must stay.
	t.Run("G3-gzip-flush-then-404", func(t *testing.T) {
		res := appsecwire.Serve(t, appsecwire.HTTP1, appsecwire.Gzip(mount(http.HandlerFunc(appsecwire.FlushThenNotFound))), appsecwire.AcceptGzip)
		assert.Equal(t, 404, res.Status)
		assert.Equal(t, appsecwire.AppBody, res.Body)
		require.Len(t, res.Spans, 1)
		assert.Equal(t, "404", res.Spans[0].Status)
	})
}

// TestAppSecResponseCommitStatusClassifier checks the span of a response that
// only calls Flush, with the status classifiers. The classifier gets the status
// that the writer recorded: 200 after a Flush that is on the wire.
func TestAppSecResponseCommitStatusClassifier(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "false")
	tests := []struct {
		name      string
		env       string
		check     func(int) bool
		wantSpan  string
		wantError bool
	}{
		{name: "S1-default", wantSpan: "200"},
		{name: "S2-check-zero", check: func(s int) bool { return s == 0 }, wantSpan: "200"},
		{name: "S3-check-200", check: func(s int) bool { return s == 200 }, wantSpan: "200", wantError: true},
		{name: "S4-env-0", env: "0", wantSpan: "200"},
		{name: "S5-env-200", env: "200", wantSpan: "200", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				// Cleanups run in reverse order: restore the environment
				// first, then read the configuration again.
				t.Cleanup(httptrace.ResetCfg)
				t.Setenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES", tt.env)
				httptrace.ResetCfg()
			}
			var opts []Option
			if tt.check != nil {
				opts = append(opts, WithStatusCheck(tt.check))
			}
			h := WrapHandler(http.HandlerFunc(appsecwire.FlushOnly), "service", "resource", opts...)
			res := appsecwire.Serve(t, appsecwire.HTTP1, h, appsecwire.Request{})
			require.Len(t, res.Spans, 1)
			assert.Equal(t, 200, res.Status)
			assert.Equal(t, tt.wantSpan, res.Spans[0].Status)
			assert.Equal(t, tt.wantError, res.Spans[0].Error)
		})
	}
}
