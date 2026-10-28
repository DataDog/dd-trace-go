// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otelc/pkg/hook/hooktest"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

// serve runs the hooks the way the trampolines do around router.ServeHTTP.
func serve(router *mux.Router, w http.ResponseWriter, req *http.Request) {
	ictx := hooktest.NewMockHookContext(router, w, req)
	BeforeServeHTTP(ictx, router, w, req)
	defer AfterServeHTTP(ictx)
	if ictx.IsSkipCall() {
		return
	}
	router.ServeHTTP(ictx.GetParam(1).(http.ResponseWriter), ictx.GetParam(2).(*http.Request))
}

func TestHooksTraceRequest(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	router := mux.NewRouter()
	router.HandleFunc("/users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	serve(router, rec, httptest.NewRequest(http.MethodGet, "/users/42", nil))

	assert.Equal(t, http.StatusTeapot, rec.Code)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/{id}", spans[0].Tag("resource.name"))
	assert.Equal(t, "gorilla/mux", spans[0].Tag("component"))
	assert.Equal(t, "418", spans[0].Tag("http.status_code"))
}

func TestHooksLetPanicThrough(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	var calls int
	router := mux.NewRouter()
	router.HandleFunc("/panic", func(http.ResponseWriter, *http.Request) {
		calls++
		panic(http.ErrAbortHandler)
	})
	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		serve(router, httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
	})
	assert.Equal(t, 1, calls)
	assert.Len(t, mt.FinishedSpans(), 1)
}

func TestConfigsFreeRouter(t *testing.T) {
	router := mux.NewRouter()
	key := weak.Make(router)
	serve(router, httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	_, ok := configs.Load(key)
	require.True(t, ok)

	router = nil
	assert.Eventually(t, func() bool {
		runtime.GC()
		_, ok := configs.Load(key)
		return key.Value() == nil && !ok
	}, 5*time.Second, 10*time.Millisecond)
}
