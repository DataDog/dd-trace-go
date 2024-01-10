// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttptrace

import (
	"context"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestHTTPHeadersCarrierSet(t *testing.T) {
	assert := assert.New(t)
	fcc := &HTTPHeadersCarrier{
		ReqHeader: new(fasthttp.RequestHeader),
	}
	t.Run("key-val", func(t *testing.T) {
		// add one item
		fcc.Set("k1", "v1")
		assert.Len(fcc.ReqHeader.PeekAll("k1"), 1)
		assert.Equal("v1", string(fcc.ReqHeader.Peek("k1")))
	})
	t.Run("key-multival", func(t *testing.T) {
		// add a second value, ensure the second value overwrites the first
		fcc.Set("k1", "v1")
		fcc.Set("k1", "v2")
		vals := fcc.ReqHeader.PeekAll("k1")
		assert.Len(vals, 1)
		assert.Equal("v2", string(vals[0]))
	})
	t.Run("multi-key", func(t *testing.T) {
		// // add a second key
		fcc.Set("k1", "v1")
		fcc.Set("k2", "v21")
		assert.Len(fcc.ReqHeader.PeekAll("k2"), 1)
		assert.Equal("v21", string(fcc.ReqHeader.Peek("k2")))
	})
	t.Run("case insensitive", func(t *testing.T) {
		// new key
		fcc.Set("K3", "v31")
		assert.Equal("v31", string(fcc.ReqHeader.Peek("k3")))
		assert.Equal("v31", string(fcc.ReqHeader.Peek("K3")))
		// access existing, lowercase key with uppercase input
		fcc.Set("K3", "v32")
		vals := fcc.ReqHeader.PeekAll("k3")
		assert.Equal("v32", string(vals[0]))
	})
}

func TestHTTPHeadersCarrierForeachKey(t *testing.T) {
	assert := assert.New(t)
	h := new(fasthttp.RequestHeader)
	headers := map[string][]string{
		"K1": {"v1"},
		"K2": {"v2", "v22"},
	}
	assert.Len(headers, 2)
	for k, vs := range headers {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	fcc := &HTTPHeadersCarrier{
		ReqHeader: h,
	}
	err := fcc.ForeachKey(func(k, v string) error {
		delete(headers, k)
		return nil
	})
	assert.NoError(err)
	assert.Len(headers, 0)
}

func TestInjectExtract(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	pspan, _ := tracer.StartSpanFromContext(context.Background(), "test")
	fcc := &HTTPHeadersCarrier{
		ReqHeader: &fasthttp.RequestHeader{},
	}
	err := tracer.Inject(pspan.Context(), fcc)
	require.NoError(t, err)
	sctx, err := tracer.Extract(fcc)
	require.NoError(t, err)
	assert.Equal(sctx.TraceID(), pspan.Context().TraceID())
	assert.Equal(sctx.SpanID(), pspan.Context().SpanID())
}
