// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type key string

func TestFromGLS(t *testing.T) {
	t.Cleanup(MockGLS())

	t.Run("Enabled() is false, ctx is nil", func(t *testing.T) {
		enabled = false
		require.Equal(t, nil, WrapContext(nil))
	})

	t.Run("Enabled() is false, ctx is not nil", func(t *testing.T) {
		enabled = false
		require.Equal(t, context.Background(), WrapContext(context.Background()))

	})

	t.Run("Enabled() is true, ctx is nil", func(t *testing.T) {
		enabled = true
		require.Equal(t, &glsContext{context.Background()}, WrapContext(nil))
	})

	t.Run("Enabled() is true, ctx is not nil", func(t *testing.T) {
		enabled = true
		ctx := context.WithValue(context.Background(), key("key"), "value")
		require.Equal(t, &glsContext{ctx}, WrapContext(ctx))
	})
}

func TestGLSPopFunc(t *testing.T) {
	t.Run("same goroutine pops value", func(t *testing.T) {
		t.Cleanup(MockGLS())

		CtxWithValue(context.Background(), key("k"), "v")
		popFn := GLSPopFunc(key("k"))

		require.Equal(t, "v", getDDContextStack().Peek(key("k")))

		popFn()

		require.Nil(t, getDDContextStack().Peek(key("k")))
	})

	t.Run("different goroutine is no-op", func(t *testing.T) {
		t.Cleanup(MockGLS())

		CtxWithValue(context.Background(), key("k"), "v")
		popFn := GLSPopFunc(key("k"))

		// Simulate a different goroutine by swapping the GLS to a new stack.
		// In production, each goroutine has its own contextStack pointer in
		// runtime.g, so getDDContextStack() returns different pointers.
		originalStack := getDDGLS()
		differentStack := contextStack(make(map[any][]any))
		setDDGLS(&differentStack)
		t.Cleanup(func() { setDDGLS(originalStack) })

		popFn()

		// Restore the original stack and verify the value was NOT popped.
		setDDGLS(originalStack)
		require.Equal(t, "v", getDDContextStack().Peek(key("k")),
			"value should not be popped when called from different goroutine")
	})

	t.Run("disabled orchestrion returns no-op", func(t *testing.T) {
		t.Cleanup(MockGLS())
		enabled = false // Override MockGLS's enabled=true to test disabled path

		popFn := GLSPopFunc(key("k"))
		popFn() // must not panic
	})
}

func TestCtxWithValue(t *testing.T) {
	t.Cleanup(MockGLS())

	t.Run("orchestrion disabled", func(t *testing.T) {
		enabled = false
		require.Equal(t, context.WithValue(context.Background(), key("key"), "value"), CtxWithValue(context.Background(), key("key"), "value"))
	})

	t.Run("orchestrion enabled", func(t *testing.T) {
		enabled = true
		ctx := CtxWithValue(context.Background(), key("key"), "value")
		require.Equal(t, context.WithValue(&glsContext{context.Background()}, key("key"), "value"), ctx)
		require.Equal(t, "value", ctx.Value(key("key")))
		require.Equal(t, "value", getDDContextStack().Peek(key("key")))
		require.Equal(t, "value", GLSPopValue(key("key")))
		require.Nil(t, getDDContextStack().Peek(key("key")))
	})

	t.Run("cross-goroutine switch", func(t *testing.T) {
		enabled = true
		ctx := CtxWithValue(context.Background(), key("key"), "value")
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Use assert (not require) from a non-test goroutine to avoid
			// calling t.FailNow which panics outside the test goroutine.
			assert.Equal(t, "value", ctx.Value(key("key")))
		}()
		wg.Wait()
	})
}
