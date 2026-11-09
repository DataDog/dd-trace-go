// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi"
	"github.com/stretchr/testify/assert"
)

// TestExistingPathBlocks checks requests that AppSec blocked before the route
// lookup existed, with a rule on a path parameter. They need no rewrite, hidden
// writer or flush: the parameters are known to chi when the tracer starts. They
// must still be blocked before the handler. These tests use only the public API
// and pass on the code before the lookup.
func TestExistingPathBlocks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    string // the parameter that the rule checks
		target string
		// wantBlocked is false when the code before the lookup did not block either.
		wantBlocked bool
		build       func(h http.HandlerFunc) http.Handler
	}{
		{
			// chi knows "id" (the mount); the child endpoint has another "id" later.
			name: "known-mount-id", key: "id", target: "/tenant/attack/items/clean", wantBlocked: true,
			build: func(h http.HandlerFunc) http.Handler {
				root, child := chi.NewRouter(), chi.NewRouter()
				child.Use(Middleware())
				child.Get("/items/{id}", h)
				root.Mount("/tenant/{id}", child)
				return root
			},
		},
		{
			// The route is final (no lookup), the name is used twice.
			name: "endpoint-duplicate-id", key: "id", target: "/tenant/attack/items/clean", wantBlocked: existingDuplicateBlock,
			build: func(h http.HandlerFunc) http.Handler {
				root, child := chi.NewRouter(), chi.NewRouter()
				child.With(Middleware()).Get("/items/{id}", h)
				root.Mount("/tenant/{id}", child)
				return root
			},
		},
		{
			// The tracer wraps the mount handler: chi has not reset the "*" yet.
			name: "with-mount-wildcard", key: "*", target: "/api/attack", wantBlocked: true,
			build: func(h http.HandlerFunc) http.Handler {
				root, child := chi.NewRouter(), chi.NewRouter()
				child.Get("/attack", h)
				root.With(Middleware()).Mount("/api", child)
				return root
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			startAppSecRules(t, pathRule("existing-path-block", "path-test", tc.key, "block"))
			ran := false
			router := tc.build(func(w http.ResponseWriter, _ *http.Request) {
				ran = true
				w.WriteHeader(http.StatusOK)
			})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.target, nil))
			if tc.wantBlocked {
				assert.Equal(t, http.StatusForbidden, w.Code)
				assert.False(t, ran, "blocked before the handler")
			} else {
				assert.Equal(t, http.StatusOK, w.Code)
				assert.True(t, ran)
			}
		})
	}
}

// existingDuplicateBlock: chi read the last value of a name that is used at
// several levels before the lookup existed, so it did not block here.
const existingDuplicateBlock = false
