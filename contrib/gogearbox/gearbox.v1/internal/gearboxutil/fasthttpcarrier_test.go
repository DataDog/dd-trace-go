// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gearboxutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestFastHTTPHeadersCarrierSet(t *testing.T) {
	assert := assert.New(t)
	fcc := &FastHTTPHeadersCarrier{
		ReqHeader: new(fasthttp.RequestHeader),
	}
	t.Run("key-val", func(t *testing.T) {
		// add one item
		fcc.Set("k1", "v1")
		assert.Len(fcc.ReqHeader.PeekAll("k1"), 1)
		assert.Equal("v1", string(fcc.ReqHeader.Peek("k1")))
	})
	t.Run("key-multival", func(t *testing.T) {
		// // add a second value
		fcc.Set("k1", "v2")
		vals := fcc.ReqHeader.PeekAll("k1")
		assert.Len(vals, 2)
		assert.Equal("v1", string(vals[0]))
		assert.Equal("v2", string(vals[1]))
	})
	t.Run("multi-key", func(t *testing.T) {
		// // add a second item
		fcc.Set("k2", "v21")
		// assert.Len(fcc.ReqHeader.PeekKeys(), 2) This returns 3 values: k1, k1 and k2...
		assert.Len(fcc.ReqHeader.PeekAll("k2"), 1)
		assert.Equal("v21", string(fcc.ReqHeader.Peek("k2")))
	})
	// FastHTTP
	t.Run("casing", func(t *testing.T) {
		// new key
		fcc.Set("K3", "v31")
		assert.Equal("v31", string(fcc.ReqHeader.Peek("k3")))
		assert.Equal("v31", string(fcc.ReqHeader.Peek("K3")))
		// existing key
		fcc.Set("K3", "v32")
		vals := fcc.ReqHeader.PeekAll("k3")
		assert.Len(vals, 2)
		assert.Equal("v32", string(vals[1]))
	})
}

func TestFastHTTPHeadersCarrierForeachKey(t *testing.T) {
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
	fcc := &FastHTTPHeadersCarrier{
		ReqHeader: h,
	}
	err := fcc.ForeachKey(func(k, v string) error {
		delete(headers, k)
		return nil
	})

	assert.Nil(err)
	assert.Len(headers, 0)
}
