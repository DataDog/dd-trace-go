// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package redigo

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"

	"github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTraceMark(t *testing.T) {
	assert.False(t, TraceMarked(context.Background()))
	assert.False(t, TraceMarked(nil))
	assert.True(t, TraceMarked(TraceMark(context.Background())))
	assert.True(t, TraceMarked(TraceMark(nil)))
}

// TestDialMarksItsOwnDial is what the otelc hook relies on. The hook fires on the
// redis.DialContext definition, which these functions call, and skips the call
// when the context is marked. Without the mark it would wrap a connection this
// package already wrapped, and every command would report twice.
//
// The dialer returns an error so no server is needed; the context has already
// been handed over by then.
func TestDialMarksItsOwnDial(t *testing.T) {
	for _, tc := range []struct {
		name string
		dial func(redis.DialOption) error
	}{
		{
			name: "Dial",
			dial: func(opt redis.DialOption) error {
				_, err := Dial("tcp", "127.0.0.1:0", opt)
				return err
			},
		},
		{
			name: "DialContext",
			dial: func(opt redis.DialOption) error {
				_, err := DialContext(context.Background(), "tcp", "127.0.0.1:0", opt)
				return err
			},
		},
		{
			name: "DialURL",
			dial: func(opt redis.DialOption) error {
				_, err := DialURL("redis://127.0.0.1:0", opt)
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got context.Context
			capture := redis.DialContextFunc(func(ctx context.Context, _, _ string) (net.Conn, error) {
				got = ctx
				return nil, errors.New("not dialling in a test")
			})

			require.Error(t, tc.dial(capture))
			require.NotNil(t, got, "the dialer was never called")
			assert.True(t, TraceMarked(got),
				"%s must mark the context it passes to redis, or otelc wraps the connection twice", tc.name)
		})
	}
}

// TestDoubleWrapEmitsTwoSpans is the behaviour the mark exists to prevent, pinned
// so the cost of losing the mark is visible. Wrapping an already traced
// connection is what an unguarded otelc hook would do.
func TestDoubleWrapEmitsTwoSpans(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	once, err := Dial("tcp", "127.0.0.1:6379")
	require.NoError(t, err)
	defer func() { assert.NoError(t, once.Close()) }()

	twice := wrapConn(once, &params{config: new(dialConfig), network: "tcp", host: "127.0.0.1", port: "6379"})
	_, err = twice.Do("SET", "double", "wrapped")
	require.NoError(t, err)

	assert.Len(t, mt.FinishedSpans(), 2,
		"a doubly wrapped connection reports each command twice; TraceMark keeps otelc from creating one")
}
