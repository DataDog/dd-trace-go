// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package mux

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
)

func TestTraceMark(t *testing.T) {
	router, other := mux.NewRouter(), mux.NewRouter()
	assert.False(t, TraceMarked(context.Background(), router))
	ctx := traceMark(context.Background(), router)
	assert.True(t, TraceMarked(ctx, router))
	assert.False(t, TraceMarked(ctx, other), "a different router must not be marked")
	assert.False(t, TraceMarked(ctx, nil))
}

// TestServeHTTPMarksItsOwnRouter is what the otelc hook relies on. The hook
// fires on mux.Router.ServeHTTP, which Router.ServeHTTP calls, and lets the call
// through when the request is marked for that router. Without the mark, a
// router wrapped with this package would get a second gorilla/mux span. Ignored
// requests are marked too, or the hook would trace them.
func TestServeHTTPMarksItsOwnRouter(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []RouterOption
	}{
		{name: "traced"},
		{name: "ignored", opts: []RouterOption{WithIgnoreRequest(func(*http.Request) bool { return true })}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := NewRouter(tc.opts...)
			var marked bool
			router.HandleFunc("/ping", func(_ http.ResponseWriter, req *http.Request) {
				marked = TraceMarked(req.Context(), router.Router)
			})
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ping", nil))
			assert.True(t, marked)
		})
	}
}
