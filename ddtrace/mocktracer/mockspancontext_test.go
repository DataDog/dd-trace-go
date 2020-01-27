// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package mocktracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMockIDs(t *testing.T) {
	last := nextID()
	for i := 0; i < 10; i++ {
		// ensure incremental (unique) IDs
		next := nextID()
		if next <= last {
			t.Fail()
		}
		last = next
	}
}

func TestSpanContextSetBaggage(t *testing.T) {
	var sc spanContext
	sc.setBaggageItem("a", "b")
	sc.setBaggageItem("c", "d")
	assert.Equal(t, sc.baggage["a"], "b")
	assert.Equal(t, sc.baggage["c"], "d")
}

func TestSpanContextGetBaggage(t *testing.T) {
	var sc spanContext
	sc.setBaggageItem("a", "b")
	sc.setBaggageItem("c", "d")
	assert.Equal(t, sc.baggageItem("a"), "b")
	assert.Equal(t, sc.baggageItem("c"), "d")
}

func TestSpanContextIterator(t *testing.T) {
	var sc spanContext
	sc.setBaggageItem("a", "b")
	sc.setBaggageItem("c", "d")

	t.Run("some", func(t *testing.T) {
		var seen int
		sc.ForeachBaggageItem(func(k, v string) bool {
			seen++
			return false
		})
		assert.Equal(t, seen, 1)
	})

	t.Run("all", func(t *testing.T) {
		seen := make(map[string]interface{}, 2)
		sc.ForeachBaggageItem(func(k, v string) bool {
			seen[k] = v
			return true
		})
		assert.Len(t, seen, 2)
		assert.Equal(t, seen["a"], "b")
		assert.Equal(t, seen["c"], "d")
	})
}
