// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package stubagent is a real HTTP trace intake for tests that run a binary
// built with otelc. The in-process agenttest.Agent does not work there: it
// delivers requests through an in-process RoundTripper and never opens a
// socket that a child process could reach.
//
// Payloads stay raw. Operation and resource names appear verbatim in the
// msgpack body, so matching on bytes avoids depending on the wire format.
package stubagent

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type Agent struct {
	server *httptest.Server

	mu       sync.Mutex
	payloads [][]byte
}

func New(t *testing.T) *Agent {
	t.Helper()

	a := &Agent{}
	mux := http.NewServeMux()
	mux.HandleFunc("/info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"endpoints":["/v0.4/traces"]}`)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if body, err := io.ReadAll(r.Body); err == nil && len(body) > 0 {
			a.mu.Lock()
			a.payloads = append(a.payloads, body)
			a.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"rate_by_service":{}}`)
	})

	a.server = httptest.NewServer(mux)
	t.Cleanup(a.server.Close)
	return a
}

// URL is the value for DD_TRACE_AGENT_URL.
func (a *Agent) URL() string {
	return a.server.URL
}

// Reported reports whether any payload contains s.
func (a *Agent) Reported(s string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.payloads {
		if bytes.Contains(p, []byte(s)) {
			return true
		}
	}
	return false
}

func (a *Agent) RequestCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.payloads)
}
