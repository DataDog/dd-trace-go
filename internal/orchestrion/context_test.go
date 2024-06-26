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

var glsValue any

func mockGLSGetterAndSetter() func() {
	prevGetDDGLS := getDDGLS
	prevSetDDGLS := setDDGLS
	prevEnabled := enabled

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

	// Test 1: Enabled() is false, ctx is nil
	enabled = false
	require.Equal(t, context.Background(), CtxOrGLS(nil))

	// Test 2: Enabled() is false, ctx is not nil
	enabled = false
	require.Equal(t, context.Background(), CtxOrGLS(context.Background()))

	// Test 3: Enabled() is true, ctx is nil, gls is nil
	enabled = true
	require.Equal(t, context.Background(), CtxOrGLS(nil))

	// Test 4: Enabled() is true, ctx is nil, gls is not nil
	enabled = true
	glsValue = context.Background()
	require.Equal(t, context.Background(), CtxOrGLS(nil))

	// Test 5: Enabled() is true, ctx is not nil
	enabled = true
	require.Equal(t, context.Background(), CtxOrGLS(context.Background()))

	// Test 6: Enabled() is true, ctx is not nil, gls is nil
	enabled = true
	glsValue = nil
	require.Equal(t, context.Background(), CtxOrGLS(context.Background()))
}

func TestCtxWithValue(t *testing.T) {

	cleanup := mockGLSGetterAndSetter()
	defer cleanup()

	// Test 1: Enabled() is false, ctx is nil
	enabled = false
	require.Equal(t, context.WithValue(context.Background(), "key", "value"), CtxWithValue(nil, "key", "value"))

	// Test 2: Enabled() is false, ctx is not nil
	enabled = false
	require.Equal(t, context.WithValue(context.Background(), "key", "value"), CtxWithValue(context.Background(), "key", "value"))

	// Test 3: Enabled() is true, ctx is nil, gls is nil
	enabled = true
	require.Equal(t, context.WithValue(context.Background(), "key", "value"), CtxWithValue(nil, "key", "value"))

	// Test 4: Enabled() is true, ctx is nil, gls is not nil
	enabled = true
	glsValue = context.Background()
	require.Equal(t, context.WithValue(context.Background(), "key", "value"), CtxWithValue(nil, "key", "value"))

	// Test 5: Enabled() is true, ctx is not nil
	enabled = true
	require.Equal(t, context.WithValue(context.Background(), "key", "value"), CtxWithValue(context.Background(), "key", "value"))

	// Test 6: Enabled() is true, ctx is not nil, gls is nil
	enabled = true
	glsValue = nil
	require.Equal(t, context.WithValue(context.Background(), "key", "value"), CtxWithValue(context.Background(), "key", "value"))

}
