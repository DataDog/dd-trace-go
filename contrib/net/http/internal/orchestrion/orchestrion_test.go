// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package orchestrion

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"
)

type testHandler struct{}

func (*testHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

// A server or a request started from another package's init function can run
// before this contrib is initialized; it is then left untraced.
func TestWrappersBeforeInitialization(t *testing.T) {
	saved := contribInitialized
	contribInitialized = func() bool { return false }
	t.Cleanup(func() { contribInitialized = saved })

	mux := http.NewServeMux()
	assert.Same(t, mux, WrapHandler(mux))
	handler := &testHandler{}
	assert.Same(t, handler, WrapHandler(handler))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/", nil)
	got, after, err := ObserveRoundTrip(req)
	require.NoError(t, err)
	assert.Same(t, req, got)
	res, wantErr := &http.Response{StatusCode: http.StatusOK}, errors.New("round trip")
	gotRes, gotErr := after(res, wantErr)
	assert.Same(t, res, gotRes)
	assert.Same(t, wantErr, gotErr)
}

func TestWrapHandlerAfterInitialization(t *testing.T) {
	require.True(t, contribInitialized())
	assert.IsType(t, &wrap.ServeMux{}, WrapHandler(http.NewServeMux()))
	_, unwrapped := WrapHandler(&testHandler{}).(*testHandler)
	assert.False(t, unwrapped)
}
