// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type key string

func mockGLSGetterAndSetter() func() {
	prevGetDDGLS := getDDGLS
	prevSetDDGLS := setDDGLS
	prevEnabled := enabled

	tmp := contextStack(make(map[any][]any))
	var glsValue any = &tmp
	getDDGLS = func() any { return glsValue }
	setDDGLS = func(a any) {
		glsValue = a
	}

	return func() {
		getDDGLS = prevGetDDGLS
		setDDGLS = prevSetDDGLS
		enabled = prevEnabled
	}
}

func TestFromGLS(t *testing.T) {
	cleanup := mockGLSGetterAndSetter()
	defer cleanup()

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

func TestCtxWithValue(t *testing.T) {
	cleanup := mockGLSGetterAndSetter()
	defer cleanup()

	t.Run("false", func(t *testing.T) {
		enabled = false
		require.Equal(t, context.WithValue(context.Background(), key("key"), "value"), CtxWithValue(context.Background(), key("key"), "value"))
	})

	t.Run("true", func(t *testing.T) {
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
		go func() {
			require.Equal(t, "value", ctx.Value(key("key")))
		}()
	})
}
