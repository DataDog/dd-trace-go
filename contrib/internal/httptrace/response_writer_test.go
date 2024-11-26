// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package httptrace

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
)

func Test_wrapResponseWriter(t *testing.T) {
	// there doesn't appear to be an easy way to test http.Pusher support via an http request
	// so we'll just confirm wrapResponseWriter preserves it
	t.Run("Pusher", func(t *testing.T) {
		var i struct {
			http.ResponseWriter
			http.Pusher
		}
		var w http.ResponseWriter = i
		_, ok := w.(http.ResponseWriter)
		assert.True(t, ok)
		_, ok = w.(http.Pusher)
		assert.True(t, ok)

		w, _ = wrapResponseWriter(w)
		_, ok = w.(http.ResponseWriter)
		assert.True(t, ok)
		_, ok = w.(http.Pusher)
		assert.True(t, ok)
	})
}

func TestBlock(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()

	if !appsec.Enabled() {
		t.Skip("appsec is not enabled")
	}

	t.Run("block-before-first-write", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		rw := newResponseWriter(recorder)
		rw.Block()
		assert.False(t, rw.blocked)

		rw.WriteHeader(http.StatusForbidden)

		rw.Block()
		assert.True(t, rw.blocked)

		assert.Equal(t, http.StatusForbidden, recorder.Code)
	})

	t.Run("write-after-block", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		rw := newResponseWriter(recorder)
		rw.WriteHeader(http.StatusForbidden)
		rw.Write([]byte("foo"))
		rw.Block()
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte("bar"))

		assert.Equal(t, http.StatusForbidden, recorder.Code)
		assert.Equal(t, recorder.Body.String(), "foo")
	})
}
