// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package waf

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateClosestToZero(t *testing.T) {
	t.Run("first_error_sets_code", func(t *testing.T) {
		var target atomic.Int32
		updateClosestToZero(&target, -2)
		require.Equal(t, int32(-2), target.Load())
	})

	t.Run("closer_to_zero_replaces", func(t *testing.T) {
		var target atomic.Int32
		updateClosestToZero(&target, -127)
		updateClosestToZero(&target, -2)
		updateClosestToZero(&target, -1)
		require.Equal(t, int32(-1), target.Load())
	})

	t.Run("further_from_zero_ignored", func(t *testing.T) {
		var target atomic.Int32
		updateClosestToZero(&target, -1)
		updateClosestToZero(&target, -127)
		require.Equal(t, int32(-1), target.Load())
	})

	t.Run("equal_code_no_change", func(t *testing.T) {
		var target atomic.Int32
		updateClosestToZero(&target, -1)
		updateClosestToZero(&target, -1)
		require.Equal(t, int32(-1), target.Load())
	})

	t.Run("sentinel_zero_always_replaced", func(t *testing.T) {
		var target atomic.Int32
		// initial zero sentinel — any error code should overwrite it
		updateClosestToZero(&target, -127)
		require.Equal(t, int32(-127), target.Load())
	})
}
