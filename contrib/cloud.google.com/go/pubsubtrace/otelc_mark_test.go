// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package pubsubtrace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPublishTraced(t *testing.T) {
	ctx := context.Background()
	assert.False(t, PublishTraced(ctx))
	assert.False(t, PublishTraced(nil)) //nolint:staticcheck // a nil context must not panic
	assert.True(t, PublishTraced(MarkPublishTraced(ctx)))

	type otherKey struct{}
	assert.True(t, PublishTraced(context.WithValue(MarkPublishTraced(ctx), otherKey{}, 1)),
		"the mark must survive contexts derived from the marked one")
}

func TestReceiveHandlerTraced(t *testing.T) {
	wrap := func(f func()) func() { return func() { f() } }

	t.Run("marked", func(t *testing.T) {
		h := wrap(func() {})
		MarkReceiveHandler(h)
		assert.True(t, ReceiveHandlerTraced(h))
	})
	t.Run("same call site", func(t *testing.T) {
		// Inlining can copy a function literal once per call site, so the
		// handlers come from one call site.
		var hs []func()
		for range 2 {
			hs = append(hs, wrap(func() {}))
		}
		MarkReceiveHandler(hs[0])
		assert.True(t, ReceiveHandlerTraced(hs[1]))
	})
	t.Run("not marked", func(t *testing.T) {
		for _, h := range []any{func() {}, nil, (func())(nil), 42} {
			assert.False(t, ReceiveHandlerTraced(h))
		}
	})
}
