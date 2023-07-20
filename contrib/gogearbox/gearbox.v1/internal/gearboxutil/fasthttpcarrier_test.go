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

func TestFasthttpCarrierSet(t *testing.T) {
	assert := assert.New(t)
	fcc := &FasthttpCarrier{
		ReqHeader: new(fasthttp.RequestHeader),
	}

	// add one item
	fcc.Set("k1", "v1")
	assert.Len(fcc.ReqHeader.PeekAll("k1"), 1)
	assert.Equal("v1", string(fcc.ReqHeader.Peek("k1")))

	// // add a second value
	fcc.Set("k1", "v2")
	vals := fcc.ReqHeader.PeekAll("k1")
	assert.Len(vals, 2)
	assert.Equal("v1", string(vals[0]))
	assert.Equal("v2", string(vals[1]))

	// // add a second item
	fcc.Set("k2", "v21")
	// assert.Len(fcc.ReqHeader.PeekKeys(), 2) This returns 3 values: k1, k1 and k2...
	assert.Len(fcc.ReqHeader.PeekAll("k2"), 1)
	assert.Equal("v21", string(fcc.ReqHeader.Peek("k2")))
}

func TestFasthttpCarrierGet(t *testing.T) {
	assert := assert.New(t)
	h := new(fasthttp.RequestHeader)
	h.Set("k1", "v1")
	h.Set("k2", "v2")
	h.Add("k2", "v22")
	fcc := &FasthttpCarrier{
		ReqHeader: h,
	}
	assert.Equal("v1", fcc.Get("k1"))
	assert.Equal("v2", fcc.Get("k2"))
}

func TestFasthttpCarrierForeachKey(t *testing.T) {
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
	fcc := &FasthttpCarrier{
		ReqHeader: h,
	}
	err := fcc.ForeachKey(func(k, v string) error {
		delete(headers, k)
		return nil
	})

	assert.Nil(err)
	assert.Len(headers, 0)
}
