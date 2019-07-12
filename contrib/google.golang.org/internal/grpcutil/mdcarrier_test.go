// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package grpcutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

func TestMDCarrierSet(t *testing.T) {
	assert := assert.New(t)
	md := metadata.MD{}
	mdc := MDCarrier(md)

	// add one item
	mdc.Set("k1", "v1")
	assert.Len(md, 1)
	assert.Len(md["k1"], 1)
	assert.Equal("v1", md["k1"][0])

	// add a second value
	mdc.Set("k1", "v2")
	assert.Len(md, 1)
	assert.Len(md["k1"], 2)
	assert.Equal("v1", md["k1"][0])
	assert.Equal("v2", md["k1"][1])

	// add a second item
	mdc.Set("k2", "v21")
	assert.Len(md, 2)
	assert.Len(md["k2"], 1)
	assert.Equal("v21", md["k2"][0])
}

func TestMDCarrierGet(t *testing.T) {
	assert := assert.New(t)
	md := metadata.Pairs("k1", "v1", "k2", "v2", "k2", "v22")
	mdc := MDCarrier(md)

	assert.Equal("v1", mdc.Get("k1"))
	assert.Equal("v2", mdc.Get("k2"))
}

func TestMDCarrierForeachKey(t *testing.T) {
	want := metadata.Pairs("k1", "v1", "k2", "v2", "k2", "v22")
	got := metadata.MD{}
	wantc := MDCarrier(want)
	gotc := MDCarrier(got)

	err := wantc.ForeachKey(func(k, v string) error {
		gotc.Set(k, v)
		return nil
	})

	assert.Nil(t, err)
	assert.Equal(t, want, got)
}
