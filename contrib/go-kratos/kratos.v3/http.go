// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package kratos

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/felixge/httpsnoop"
)

type httpResponseKey struct{}

type httpResponseState struct {
	mu       sync.Mutex
	status   int
	hijacked bool
	finished bool
	pending  []func(int)
}

func (s *httpResponseState) record(code int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == 0 && (code >= 200 || code == http.StatusSwitchingProtocols) {
		s.status = code
	}
}

func (s *httpResponseState) register(fn func(int)) {
	s.mu.Lock()
	if !s.finished {
		s.pending = append(s.pending, fn)
		s.mu.Unlock()
		return
	}
	code := s.status
	s.mu.Unlock()
	fn(code)
}

func (s *httpResponseState) finish(completed bool) {
	s.mu.Lock()
	if completed && !s.hijacked && s.status == 0 {
		s.status = http.StatusOK
	}
	s.finished = true
	pending, code := s.pending, s.status
	s.pending = nil
	s.mu.Unlock()
	for _, fn := range pending {
		fn(code)
	}
}

// HTTPServerFilter observes the final HTTP response and finishes Server spans
// after response encoding. Register it with the Kratos HTTP Filter option in
// addition to Server middleware. Orchestrion installs both automatically.
// Without this filter, successful responses whose status is unavailable omit
// the status tag instead of assuming 200.
func HTTPServerFilter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Value(httpResponseKey{}).(*httpResponseState); ok {
			next.ServeHTTP(w, r)
			return
		}
		state := new(httpResponseState)
		completed := false
		defer func() { state.finish(completed) }()
		wrapped := httpsnoop.Wrap(w, httpsnoop.Hooks{
			WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
				return func(code int) {
					next(code)
					state.record(code)
				}
			},
			Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
				return func(p []byte) (int, error) {
					state.record(http.StatusOK)
					return next(p)
				}
			},
			ReadFrom: func(next httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
				return func(r io.Reader) (int64, error) {
					state.record(http.StatusOK)
					return next(r)
				}
			},
			Flush: func(next httpsnoop.FlushFunc) httpsnoop.FlushFunc {
				return func() {
					state.record(http.StatusOK)
					next()
				}
			},
			Hijack: func(next httpsnoop.HijackFunc) httpsnoop.HijackFunc {
				return func() (net.Conn, *bufio.ReadWriter, error) {
					conn, rw, err := next()
					if err == nil {
						state.mu.Lock()
						state.hijacked = true
						state.mu.Unlock()
					}
					return conn, rw, err
				}
			},
		})
		next.ServeHTTP(wrapped, r.WithContext(context.WithValue(r.Context(), httpResponseKey{}, state)))
		completed = true
	})
}
