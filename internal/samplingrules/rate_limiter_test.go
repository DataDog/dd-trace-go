// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package samplingrules

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

const testDefaultRateLimit = 100.0

func TestSamplingLimiter(t *testing.T) {
	t.Run("resets-every-second", func(t *testing.T) {
		assert := assert.New(t)
		sl := NewRateLimiter(testDefaultRateLimit)
		sl.PrevSeen = 100
		sl.PrevAllowed = 99
		sl.Allowed = 42
		sl.Seen = 100
		// exact point it should reset
		now := time.Now().Add(1 * time.Second)

		sampled, _ := sl.AllowOne(now)
		assert.True(sampled)
		assert.Equal(42.0, sl.PrevAllowed)
		assert.Equal(100.0, sl.PrevSeen)
		assert.Equal(now, sl.PrevTime)
		assert.Equal(1.0, sl.Seen)
		assert.Equal(1.0, sl.Allowed)
	})

	t.Run("averages-rates", func(t *testing.T) {
		assert := assert.New(t)
		sl := NewRateLimiter(testDefaultRateLimit)
		sl.PrevSeen = 100
		sl.PrevAllowed = 42
		sl.Allowed = 41
		sl.Seen = 99
		// this event occurs within the current period
		now := sl.PrevTime

		sampled, rate := sl.AllowOne(now)
		assert.True(sampled)
		assert.Equal(0.42, rate)
		assert.Equal(now, sl.PrevTime)
		assert.Equal(100.0, sl.Seen)
		assert.Equal(42.0, sl.Allowed)
	})

	t.Run("discards-rate", func(t *testing.T) {
		assert := assert.New(t)
		sl := NewRateLimiter(testDefaultRateLimit)
		sl.PrevSeen = 100
		sl.PrevAllowed = 42
		sl.Allowed = 42
		sl.Seen = 100
		// exact point it should discard previous rate
		now := time.Now().Add(2 * time.Second)

		sampled, _ := sl.AllowOne(now)
		assert.True(sampled)
		assert.Equal(0.0, sl.PrevSeen)
		assert.Equal(0.0, sl.PrevAllowed)
		assert.Equal(now, sl.PrevTime)
		assert.Equal(1.0, sl.Seen)
		assert.Equal(1.0, sl.Allowed)
	})
}
