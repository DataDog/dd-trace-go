// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package redis

import (
	"context"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
)

func enableOtelc(t *testing.T) {
	otelcEnabled = func() bool { return true }
	t.Cleanup(func() { otelcEnabled = func() bool { return false } })
}

func TestMarkOrSkip(t *testing.T) {
	ring, shard := &datadogHook{}, &datadogHook{}
	ctx := context.Background()
	get, set := redis.NewStringCmd(ctx, "get", "k"), redis.NewStatusCmd(ctx, "set", "k", "v")

	t.Run("plain build", func(t *testing.T) {
		ctx, skip := markOrSkip(ctx, ring, get)
		assert.False(t, skip)
		_, skip = markOrSkip(ctx, shard, get)
		assert.False(t, skip, "plain builds must not mark or skip")
	})

	t.Run("otelc build", func(t *testing.T) {
		enableOtelc(t)

		ctx, skip := markOrSkip(ctx, ring, get)
		assert.False(t, skip, "the first hook traces the command")
		_, skip = markOrSkip(ctx, shard, get)
		assert.True(t, skip, "a nested hook must skip the same command")
		_, skip = markOrSkip(ctx, ring, get)
		assert.False(t, skip, "the marking hook must not skip its own command")
		_, skip = markOrSkip(ctx, shard, set)
		assert.False(t, skip, "another command sent with the same context must be traced")
	})

	t.Run("otelc build pipeline", func(t *testing.T) {
		enableOtelc(t)

		ctx, skip := markOrSkipPipeline(ctx, ring, []redis.Cmder{get, set})
		assert.False(t, skip)
		_, skip = markOrSkipPipeline(ctx, shard, []redis.Cmder{set})
		assert.True(t, skip, "a node's part of the pipeline must be skipped")
		_, skip = markOrSkipPipeline(ctx, shard, []redis.Cmder{redis.NewStringCmd(ctx, "get", "other")})
		assert.False(t, skip, "another pipeline sent with the same context must be traced")
	})
}

func TestReplaceHook(t *testing.T) {
	ctx := context.Background()
	get := redis.NewStringCmd(ctx, "get", "k")

	t.Run("plain build", func(t *testing.T) {
		client := redis.NewClient(&redis.Options{})
		defer client.Close()
		first, second := &datadogHook{}, &datadogHook{}
		replaceHook(client, first)
		replaceHook(client, second)
		_, skip := markOrSkip(ctx, first, get)
		assert.False(t, skip)
	})

	t.Run("otelc build", func(t *testing.T) {
		enableOtelc(t)
		client := redis.NewClient(&redis.Options{})
		defer client.Close()
		other := redis.NewClient(&redis.Options{})
		defer other.Close()

		auto, explicit, otherHook := &datadogHook{}, &datadogHook{}, &datadogHook{}
		replaceHook(client, auto)
		replaceHook(other, otherHook)
		replaceHook(client, explicit)

		_, skip := markOrSkip(ctx, auto, get)
		assert.True(t, skip, "the replaced hook must not trace")
		_, skip = markOrSkip(ctx, explicit, get)
		assert.False(t, skip, "the application's hook must trace")
		_, skip = markOrSkip(ctx, otherHook, get)
		assert.False(t, skip, "a hook on another client must not be replaced")
	})
}

func TestTracedBy(t *testing.T) {
	ring, shard := &datadogHook{}, &datadogHook{}
	ctx := context.Background()
	get := redis.NewStringCmd(ctx, "get", "k")

	assert.True(t, tracedBy(ctx, shard), "plain builds always finish their span")

	enableOtelc(t)
	ctx, _ = markOrSkip(ctx, ring, get)
	ctx, _ = markOrSkip(ctx, shard, get)
	assert.True(t, tracedBy(ctx, ring))
	assert.False(t, tracedBy(ctx, shard), "a hook that skipped must not finish a span")
}
