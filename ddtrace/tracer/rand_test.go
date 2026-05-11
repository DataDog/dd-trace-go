// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSecureRandom(t *testing.T) {
	// Save and restore the package-level secureRandom flag.
	orig := secureRandom
	defer func() { secureRandom = orig }()

	t.Run("csrngUint64 produces non-zero values", func(t *testing.T) {
		for range 10 {
			assert.NotEqual(t, uint64(0), csrngUint64())
		}
	})

	t.Run("csrngUint64 produces varied values", func(t *testing.T) {
		seen := make(map[uint64]bool)
		for range 100 {
			seen[csrngUint64()] = true
		}
		// Allow a tiny collision probability; in practice all 100 should be unique.
		assert.Greater(t, len(seen), 90)
	})

	t.Run("randUint64 uses csrng when secureRandom=true", func(t *testing.T) {
		secureRandom = true
		v := randUint64()
		assert.NotEqual(t, uint64(0), v)
	})

	t.Run("randUint64 uses math/rand when secureRandom=false", func(t *testing.T) {
		secureRandom = false
		// Just verify it returns without panic and produces a value.
		_ = randUint64()
	})

	t.Run("generateSpanID bounded when secureRandom=true", func(t *testing.T) {
		secureRandom = true
		for range 100 {
			id := generateSpanID(0)
			assert.LessOrEqual(t, id, uint64(math.MaxInt64))
			assert.Greater(t, id, uint64(0))
		}
	})

	t.Run("generateSpanID bounded when secureRandom=false", func(t *testing.T) {
		secureRandom = false
		for range 100 {
			id := generateSpanID(0)
			assert.LessOrEqual(t, id, uint64(math.MaxInt64))
		}
	})
}
