// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

type key string

func mockGLSGetterAndSetter() func() {
	prevGetDDGLS := getDDGLS
	prevSetDDGLS := setDDGLS
	prevEnabled := enabled

	var glsValue any
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

func TestCtxOrGLS(t *testing.T) {
	cleanup := mockGLSGetterAndSetter()
	defer cleanup()

	t.Run("Enabled() is false, ctx is nil", func(t *testing.T) {
		enabled = false
		require.Equal(t, context.Background(), CtxOrGLS(nil))
	})

	t.Run("Enabled() is false, ctx is not nil", func(t *testing.T) {
		enabled = false
		require.Equal(t, context.Background(), CtxOrGLS(context.Background()))

	})

	t.Run("Enabled() is true, ctx is nil, gls is nil", func(t *testing.T) {
		enabled = true
		require.Equal(t, context.Background(), CtxOrGLS(nil))
	})

	t.Run("Enabled() is true, ctx is nil, gls is not nil", func(t *testing.T) {
		enabled = true
		setDDGLS(context.Background())
		require.Equal(t, context.Background(), CtxOrGLS(nil))
	})

	t.Run("Enabled() is true, ctx is not nil", func(t *testing.T) {
		enabled = true
		require.Equal(t, context.Background(), CtxOrGLS(context.Background()))
	})

	t.Run("Enabled() is true, ctx is not nil, gls is nil", func(t *testing.T) {
		enabled = true
		setDDGLS(nil)
		require.Equal(t, context.Background(), CtxOrGLS(context.Background()))
	})
}

func TestCtxWithValue(t *testing.T) {
	cleanup := mockGLSGetterAndSetter()
	defer cleanup()

	t.Run("Enabled() is false, ctx is nil", func(t *testing.T) {
		enabled = false
		require.Equal(t, context.WithValue(context.Background(), key("key"), "value"), CtxWithValue(nil, key("key"), "value"))
	})

	t.Run("Enabled() is false, ctx is not nil", func(t *testing.T) {
		enabled = false
		require.Equal(t, context.WithValue(context.Background(), key("key"), "value"), CtxWithValue(context.Background(), key("key"), "value"))
	})

	t.Run("Enabled() is true, ctx is nil, gls is nil", func(t *testing.T) {
		enabled = true
		require.Equal(t, context.WithValue(context.Background(), key("key"), "value"), CtxWithValue(nil, key("key"), "value"))
	})

	t.Run("Enabled() is true, ctx is nil, gls is not nil", func(t *testing.T) {
		enabled = true
		setDDGLS(context.Background())
		require.Equal(t, context.WithValue(context.Background(), key("key"), "value"), CtxWithValue(nil, key("key"), "value"))
	})

	t.Run("Enabled() is true, ctx is not nil", func(t *testing.T) {
		enabled = true
		require.Equal(t, context.WithValue(context.Background(), key("key"), "value"), CtxWithValue(context.Background(), key("key"), "value"))
	})

	t.Run("Enabled() is true, ctx is not nil, gls is nil", func(t *testing.T) {
		enabled = true
		setDDGLS(nil)
		require.Equal(t, context.WithValue(context.Background(), key("key"), "value"), CtxWithValue(context.Background(), key("key"), "value"))
	})
}
