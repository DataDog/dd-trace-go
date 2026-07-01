// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFlattenContextNested is a regression guard: legitimate nested context still flattens to
// dot notation exactly as before the depth/cycle hardening was added.
func TestFlattenContextNested(t *testing.T) {
	in := map[string]any{
		"user": map[string]any{"id": "123", "email": "a@b.com"},
		"plan": "pro",
	}
	got := flattenContext(in)
	assert.Equal(t, map[string]any{
		"user.id":    "123",
		"user.email": "a@b.com",
		"plan":       "pro",
	}, got)
}

// TestFlattenContextSelfReferenceDoesNotCrash verifies that a directly self-referential
// context does not recurse forever. In Go a stack overflow is fatal (not recoverable), so a
// missing guard would crash the whole process rather than fail this assertion.
func TestFlattenContextSelfReferenceDoesNotCrash(t *testing.T) {
	m := map[string]any{"name": "leo"}
	m["self"] = m // cycle: m -> m

	got := flattenContext(m)

	assert.Equal(t, "leo", got["name"])
	// The cyclic back-reference must be broken, never expanded.
	for k := range got {
		assert.NotContains(t, k, "self.self")
	}
}

// TestFlattenContextIndirectCycleDoesNotCrash covers an a -> b -> a cycle across two maps.
func TestFlattenContextIndirectCycleDoesNotCrash(t *testing.T) {
	a := map[string]any{"leaf": "x"}
	b := map[string]any{"a": a}
	a["b"] = b // a -> b -> a

	got := flattenContext(map[string]any{"root": a})

	assert.Equal(t, "x", got["root.leaf"])
}

// TestFlattenContextSharedSubtreeNotTreatedAsCycle ensures cycle detection does not eat a
// legitimate diamond (the same sub-map referenced from two sibling keys).
func TestFlattenContextSharedSubtreeNotTreatedAsCycle(t *testing.T) {
	shared := map[string]any{"v": "1"}
	in := map[string]any{"x": shared, "y": shared}

	got := flattenContext(in)

	assert.Equal(t, "1", got["x.v"])
	assert.Equal(t, "1", got["y.v"])
}

// TestFlattenContextDeepMapBounded verifies nesting beyond maxContextDepth is dropped rather
// than exhausting the stack.
func TestFlattenContextDeepMapBounded(t *testing.T) {
	deep := map[string]any{"leaf": "deep-value"}
	for i := 0; i < maxContextDepth+10; i++ {
		deep = map[string]any{"n": deep}
	}

	got := flattenContext(deep)

	// The leaf sits below the depth cap, so it must not appear, and no key may exceed the cap.
	for k, v := range got {
		assert.NotEqual(t, "deep-value", v)
		assert.LessOrEqual(t, strings.Count(k, "."), maxContextDepth)
	}
}

// TestFlattenContextDeepArrayBounded is the array analogue of the deep-map bound.
func TestFlattenContextDeepArrayBounded(t *testing.T) {
	var deep any = "leaf-value"
	for i := 0; i < maxContextDepth+10; i++ {
		deep = []any{deep}
	}

	got := flattenContext(map[string]any{"arr": deep})

	for _, v := range got {
		assert.NotEqual(t, "leaf-value", v)
	}
}
