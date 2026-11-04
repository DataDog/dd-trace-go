// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package httprouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

func TestTraceMark(t *testing.T) {
	router, other := httprouter.New(), httprouter.New()
	assert.False(t, TraceMarked(context.Background(), router))
	ctx := traceMark(context.Background(), router)
	assert.True(t, TraceMarked(ctx, router))
	assert.False(t, TraceMarked(ctx, other), "a different router must not be marked")
	assert.False(t, TraceMarked(ctx, nil))
}

func TestWrapRouter(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	inner := httprouter.New()
	inner.GET("/users/:id", func(w http.ResponseWriter, _ *http.Request, _ httprouter.Params) {
		w.WriteHeader(http.StatusOK)
	})
	router := WrapRouter(inner)
	require.Same(t, inner, router.Router)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/42", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/:id", spans[0].Tag(ext.ResourceName))
	assert.Equal(t, string(instrumentation.PackageJulienschmidtHTTPRouter), spans[0].Tag(ext.Component))
}
