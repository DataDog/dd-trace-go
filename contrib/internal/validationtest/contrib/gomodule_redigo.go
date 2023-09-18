// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package validationtest

import (
	"context"
	"testing"

	redigotrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/gomodule/redigo"

	"github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/require"
)

type GoModuleRedigo struct {
	client   redis.Conn
	numSpans int
	opts     []redigotrace.DialOption
}

func NewGoModuleRedigo() *GoModuleRedigo {
	return &GoModuleRedigo{
		opts: make([]redigotrace.DialOption, 0),
	}
}

func (i *GoModuleRedigo) WithServiceName(name string) {
	i.opts = append(i.opts, redigotrace.WithServiceName(name))
}

func (i *GoModuleRedigo) Name() string {
	return "garyburd/redigo"
}

func (i *GoModuleRedigo) Init(t *testing.T) {
	t.Helper()
	client, err := redigotrace.Dial("tcp", "127.0.0.1:6379", i.opts)
	i.client = client
	require.NoError(t, err)

	t.Cleanup(func() {
		i.client.Close()
		i.numSpans = 0
	})
}

func (i *GoModuleRedigo) GenSpans(t *testing.T) {
	t.Helper()

	i.client.Do("SET", 1, "truck")
	i.numSpans++

	_, err := i.client.Do("NOT_A_COMMAND", context.Background())
	require.Error(t, err)
	i.numSpans++

	pool := &redis.Pool{
		MaxIdle:     2,
		MaxActive:   3,
		IdleTimeout: 23,
		Wait:        true,
		Dial: func() (redis.Conn, error) {
			return redigotrace.Dial("tcp", "127.0.0.1:6379")
		},
	}

	pc := pool.Get()
	pc.Do("SET", " whiskey", " glass", context.Background())
	i.numSpans++
}

func (i *GoModuleRedigo) NumSpans() int {
	return i.numSpans
}
