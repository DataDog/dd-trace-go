// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package appsecwire

import (
	"net/http"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/appsec"
)

// AppBody is the body that the application writes.
const AppBody = "APP-BODY"

const rawResponse = "HTTP/1.1 200 OK\r\nContent-Length: 8\r\nConnection: close\r\n\r\n" + AppBody

// The handlers below all set the response header that the WAF blocks (X-Block:
// attack), then commit the response in a different way.

func markBlock(w http.ResponseWriter) { w.Header().Set("X-Block", "attack") }

// NoWrite writes nothing.
func NoWrite(w http.ResponseWriter, _ *http.Request) { markBlock(w) }

// WriteOnly writes the body, with no Flush.
func WriteOnly(w http.ResponseWriter, _ *http.Request) {
	markBlock(w)
	_, _ = w.Write([]byte(AppBody))
}

// FlushOnly calls Flush, and nothing else.
func FlushOnly(w http.ResponseWriter, _ *http.Request) {
	markBlock(w)
	w.(http.Flusher).Flush()
}

// FlushThenWrite calls Flush, then writes the body.
func FlushThenWrite(w http.ResponseWriter, _ *http.Request) {
	markBlock(w)
	w.(http.Flusher).Flush()
	_, _ = w.Write([]byte(AppBody))
}

// WriteHeaderThenFlush calls WriteHeader(200), then Flush.
func WriteHeaderThenFlush(w http.ResponseWriter, _ *http.Request) {
	markBlock(w)
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
}

// WriteThenFlush writes the body, then calls Flush.
func WriteThenFlush(w http.ResponseWriter, _ *http.Request) {
	markBlock(w)
	_, _ = w.Write([]byte(AppBody))
	w.(http.Flusher).Flush()
}

// FlushThenNotFound calls Flush, then WriteHeader(404) and writes the body.
func FlushThenNotFound(w http.ResponseWriter, _ *http.Request) {
	markBlock(w)
	w.(http.Flusher).Flush()
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(AppBody))
}

// ControllerFlush calls Flush through a [http.ResponseController].
func ControllerFlush(w http.ResponseWriter, _ *http.Request) {
	markBlock(w)
	if err := http.NewResponseController(w).Flush(); err != nil {
		panic(err)
	}
}

// HijackRaw hijacks the connection and writes a raw HTTP/1.1 200 response.
func HijackRaw(w http.ResponseWriter, _ *http.Request) {
	markBlock(w)
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		panic(err)
	}
	_, _ = conn.Write([]byte(rawResponse))
	_ = conn.Close()
}

// HijackNoWrite hijacks the connection and closes it.
func HijackNoWrite(w http.ResponseWriter, _ *http.Request) {
	markBlock(w)
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		panic(err)
	}
	_ = conn.Close()
}

// ControllerHijackNoWrite hijacks the connection through a
// [http.ResponseController] and closes it.
func ControllerHijackNoWrite(w http.ResponseWriter, _ *http.Request) {
	markBlock(w)
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		panic(err)
	}
	_ = conn.Close()
}

// ControllerHijackRaw hijacks the connection through a
// [http.ResponseController] and writes a raw HTTP/1.1 200 response.
func ControllerHijackRaw(w http.ResponseWriter, _ *http.Request) {
	markBlock(w)
	conn, _, err := http.NewResponseController(w).Hijack()
	if err != nil {
		panic(err)
	}
	_, _ = conn.Write([]byte(rawResponse))
	_ = conn.Close()
}

// FailedControllerHijack tries to hijack the connection through a
// [http.ResponseController]. It panics if Hijack succeeds.
func FailedControllerHijack(w http.ResponseWriter, _ *http.Request) {
	markBlock(w)
	if _, _, err := http.NewResponseController(w).Hijack(); err == nil {
		panic("Hijack must fail")
	}
}

// Redirect sets the response header that the WAF redirects, and writes nothing.
func Redirect(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("X-Redirect", "attack")
}

// SetUserBlocked sets a user that the WAF blocks, before any write. It writes
// AppBody only if the user was not blocked.
func SetUserBlocked(w http.ResponseWriter, r *http.Request) {
	if err := appsec.SetUser(r.Context(), "blocked-user"); err != nil {
		return
	}
	_, _ = w.Write([]byte(AppBody))
}

// Ran returns a handler that records that h ran.
func Ran(t *testing.T, h http.Handler, ran *bool) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*ran = true
		h.ServeHTTP(w, r)
	})
}
