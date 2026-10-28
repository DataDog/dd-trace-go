// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package redis

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMarkOrSkip(t *testing.T) {
	ring, shard := &datadogHook{}, &datadogHook{}

	t.Run("plain build", func(t *testing.T) {
		ctx, skip := markOrSkip(context.Background(), ring)
		assert.False(t, skip)
		_, skip = markOrSkip(ctx, shard)
		assert.False(t, skip, "plain builds must not mark or skip")
	})

	t.Run("otelc build", func(t *testing.T) {
		otelcEnabled = func() bool { return true }
		defer func() { otelcEnabled = func() bool { return false } }()

		ctx, skip := markOrSkip(context.Background(), ring)
		assert.False(t, skip, "the first hook traces the command")
		_, skip = markOrSkip(ctx, shard)
		assert.True(t, skip, "a nested hook must skip")
		assert.True(t, tracedByOther(ctx, shard))
		_, skip = markOrSkip(ctx, ring)
		assert.False(t, skip, "the marking hook must not skip its own command")
		assert.False(t, tracedByOther(ctx, ring))
	})
}
