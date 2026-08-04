// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package otelc

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otelc/pkg/hook/hooktest"

	redigotrace "github.com/DataDog/dd-trace-go/contrib/gomodule/redigo/v2"
)

// listen starts a socket that accepts connections and never speaks. Dialing is
// enough for these tests: redigo only exchanges commands at dial time when
// authentication or database options are set.
func listen(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var (
		mu       sync.Mutex
		accepted []net.Conn
	)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			accepted = append(accepted, conn)
			mu.Unlock()
		}
	}()

	t.Cleanup(func() {
		assert.NoError(t, ln.Close())
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range accepted {
			_ = conn.Close()
		}
	})
	return ln.Addr().String()
}

func isTraced(conn redis.Conn) bool {
	switch conn.(type) {
	case redigotrace.Conn, redigotrace.ConnWithTimeout, redigotrace.ConnWithContext:
		return true
	default:
		return false
	}
}

func TestDialContextHook(t *testing.T) {
	addr := listen(t)

	ictx := hooktest.NewMockHookContext()
	BeforeDialContext(ictx, context.Background(), "tcp", addr)
	require.True(t, ictx.SkipCall, "the original dial must be replaced")

	AfterDialContext(ictx, nil, nil)
	require.Len(t, ictx.ReturnVals, 1)

	conn, ok := ictx.ReturnVals[0].(redis.Conn)
	require.True(t, ok, "first return value must be a redis.Conn, got %T", ictx.ReturnVals[0])
	assert.True(t, isTraced(conn), "connection is not traced, got %T", conn)
	assert.NoError(t, conn.Close())
}

func TestDialContextHookReentrant(t *testing.T) {
	addr := listen(t)

	// The contrib marks the dial it makes itself. Letting a marked dial through
	// is what keeps the hook from recursing and from double-wrapping a connection
	// the caller already wrapped.
	ctx := redigotrace.TraceMark(context.Background())

	ictx := hooktest.NewMockHookContext()
	BeforeDialContext(ictx, ctx, "tcp", addr)
	assert.False(t, ictx.SkipCall, "the original dial must run")

	AfterDialContext(ictx, nil, nil)
	assert.Empty(t, ictx.ReturnVals, "return values must be left untouched")
}

func TestDialContextHookError(t *testing.T) {
	ictx := hooktest.NewMockHookContext()
	// Port 1 is reserved, so the dial is refused rather than merely slow.
	BeforeDialContext(ictx, context.Background(), "tcp", "127.0.0.1:1")
	require.True(t, ictx.SkipCall)

	AfterDialContext(ictx, nil, nil)
	require.Len(t, ictx.ReturnVals, 2)

	assert.Nil(t, ictx.ReturnVals[0])
	err, ok := ictx.ReturnVals[1].(error)
	require.True(t, ok, "second return value must be an error, got %T", ictx.ReturnVals[1])
	assert.Error(t, err)
}
