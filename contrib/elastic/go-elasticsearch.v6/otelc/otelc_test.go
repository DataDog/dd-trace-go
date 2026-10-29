// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"io"
	"net/http"
	"strings"
	"testing"

	elastictrace "github.com/DataDog/dd-trace-go/contrib/elastic/go-elasticsearch.v6/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestReady(t *testing.T) {
	assert.True(t, Ready())
}

func TestTransport(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		assert.True(t, elastictrace.IsTraced(Transport(nil, nil, "")))
	})

	t.Run("custom", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		var calls int
		base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}")), Request: req}, nil
		})
		rt := Transport(base, nil, "")
		require.True(t, elastictrace.IsTraced(rt))

		req, err := http.NewRequest(http.MethodGet, "http://localhost:9200/index", nil)
		require.NoError(t, err)
		res, err := rt.RoundTrip(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())

		assert.Equal(t, 1, calls)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Equal(t, "GET /index", spans[0].Tag("resource.name"))
	})

	t.Run("traced", func(t *testing.T) {
		rt := elastictrace.NewRoundTripper(elastictrace.WithService("custom"))
		assert.Same(t, rt, Transport(rt, nil, ""))
	})

	t.Run("ca cert", func(t *testing.T) {
		assert.Nil(t, Transport(nil, []byte("cert"), ""))
		assert.Equal(t, http.DefaultTransport, Transport(http.DefaultTransport, []byte{}, ""))
	})

	t.Run("fingerprint", func(t *testing.T) {
		assert.Nil(t, Transport(nil, nil, "ab12"))
		assert.Equal(t, http.DefaultTransport, Transport(http.DefaultTransport, nil, "ab12"))
	})
}
