// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package validationtest

import (
	"testing"

	memcachetrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/bradfitz/gomemcache/memcache"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/stretchr/testify/require"
)

type Memcache struct {
	client   *memcachetrace.Client
	opts     []memcachetrace.ClientOption
	numSpans int
}

func NewMemcache() *Memcache {
	return &Memcache{
		opts: make([]memcachetrace.ClientOption, 0),
	}
}

func (i *Memcache) Name() string {
	return "bradfitz/gomemcache/memcache"
}

func (i *Memcache) Init(_ *testing.T) {
	i.client = memcachetrace.WrapClient(memcache.New("127.0.0.1:11211"), i.opts...)
}

func (i *Memcache) GenSpans(t *testing.T) {
	t.Helper()
	err := i.client.Set(&memcache.Item{Key: "myKey", Value: []byte("myValue")})
	require.NoError(t, err)
	i.numSpans++
	t.Cleanup(func() {
		i.numSpans = 0
	})
}

func (i *Memcache) NumSpans() int {
	return i.numSpans
}

func (i *Memcache) WithServiceName(name string) {
	i.opts = append(i.opts, memcachetrace.WithServiceName(name))
}
