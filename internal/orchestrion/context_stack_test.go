// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package orchestrion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stackTestKey struct{}

func TestPopNilsBackingArrayElement(t *testing.T) {
	s := contextStack(make(map[any][]any))

	// Push two values so popping one keeps the map entry alive (len > 0).
	// This lets us inspect the backing array for the cleared slot.
	s.Push(stackTestKey{}, "filler")
	large := make([]byte, 1<<20) // 1 MiB
	s.Push(stackTestKey{}, large)

	popped := s.Pop(stackTestKey{})
	require.NotNil(t, popped)

	// The map entry still exists (one element remains). Check that the
	// backing array slot at index 1 was cleared so GC can collect it.
	stack := s[stackTestKey{}]
	require.Len(t, stack, 1, "one element should remain")
	rawSlice := stack[:cap(stack)]
	assert.Nil(t, rawSlice[1], "popped element should be nil in backing array to allow GC")
}

func TestPopCleansUpEmptyMapEntry(t *testing.T) {
	s := contextStack(make(map[any][]any))

	s.Push(stackTestKey{}, "value")
	s.Pop(stackTestKey{})

	_, exists := s[stackTestKey{}]
	assert.False(t, exists, "empty stack entry should be removed from the map")
}
