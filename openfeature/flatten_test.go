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

// TestFlattenContext exercises both the normal flattening behavior and the traversal bounds
// (depth cap, cycle detection, and the total-field safety ceiling) that keep an
// attacker-influenceable evaluation context from exhausting the stack or memory.
//
// Cyclic / self-referential / deeply nested contexts cannot be expressed as map literals, so each
// case builds its input via a closure and verifies the result via a closure. Exact-output cases
// assert equality; the safety-bound cases assert the result is bounded (and, critically, that the
// call returns at all — an unbounded traversal would fatally overflow the Go stack or OOM the
// process rather than fail an assertion).
func TestFlattenContext(t *testing.T) {
	tests := []struct {
		name   string
		build  func() map[string]any
		verify func(t *testing.T, got map[string]any)
	}{
		{
			name: "nested maps flatten to dot notation",
			build: func() map[string]any {
				return map[string]any{
					"user": map[string]any{"id": "123", "email": "a@b.com"},
					"plan": "pro",
				}
			},
			verify: func(t *testing.T, got map[string]any) {
				assert.Equal(t, map[string]any{
					"user.id":    "123",
					"user.email": "a@b.com",
					"plan":       "pro",
				}, got)
			},
		},
		{
			name: "nested arrays flatten with indices",
			build: func() map[string]any {
				return map[string]any{"tags": []any{"a", "b"}}
			},
			verify: func(t *testing.T, got map[string]any) {
				assert.Equal(t, map[string]any{"tags.0": "a", "tags.1": "b"}, got)
			},
		},
		{
			name: "direct self-reference is skipped, siblings kept",
			build: func() map[string]any {
				m := map[string]any{"name": "leo"}
				m["self"] = m // cycle: m -> m
				return m
			},
			verify: func(t *testing.T, got map[string]any) {
				// The cyclic branch is dropped entirely; the scalar sibling survives.
				assert.Equal(t, map[string]any{"name": "leo"}, got)
			},
		},
		{
			name: "indirect cycle a -> b -> a is broken",
			build: func() map[string]any {
				a := map[string]any{"leaf": "x"}
				b := map[string]any{"a": a}
				a["b"] = b // a -> b -> a
				return map[string]any{"root": a}
			},
			verify: func(t *testing.T, got map[string]any) {
				assert.Equal(t, map[string]any{"root.leaf": "x"}, got)
			},
		},
		{
			name: "shared subtree (diamond) is not treated as a cycle",
			build: func() map[string]any {
				shared := map[string]any{"v": "1"}
				return map[string]any{"x": shared, "y": shared}
			},
			verify: func(t *testing.T, got map[string]any) {
				// Both sibling branches must fully flatten — cycle detection only breaks
				// references that are live on the current recursion stack.
				assert.Equal(t, map[string]any{"x.v": "1", "y.v": "1"}, got)
			},
		},
		{
			name: "map nesting beyond the depth cap is dropped",
			build: func() map[string]any {
				deep := map[string]any{"leaf": "deep-value"}
				for range maxContextDepth + 10 {
					deep = map[string]any{"n": deep}
				}
				return deep
			},
			verify: func(t *testing.T, got map[string]any) {
				for k, v := range got {
					assert.NotEqual(t, "deep-value", v, "leaf below the depth cap must be dropped")
					assert.LessOrEqual(t, strings.Count(k, "."), maxContextDepth)
				}
			},
		},
		{
			name: "array nesting beyond the depth cap is dropped",
			build: func() map[string]any {
				var deep any = "leaf-value"
				for range maxContextDepth + 10 {
					deep = []any{deep}
				}
				return map[string]any{"arr": deep}
			},
			verify: func(t *testing.T, got map[string]any) {
				for _, v := range got {
					assert.NotEqual(t, "leaf-value", v, "leaf below the depth cap must be dropped")
				}
			},
		},
		{
			name: "shared-subtree fan-out is bounded by the field ceiling",
			build: func() map[string]any {
				// Branching-2 nesting reusing the same child at each level would expand to ~2^20
				// leaves without the ceiling; depth (20) stays under maxContextDepth so the depth
				// cap alone does not save us here — only maxFlattenFields does.
				var cur any = map[string]any{"x": "v"}
				for range 20 {
					cur = map[string]any{"a": cur, "b": cur}
				}
				return map[string]any{"root": cur}
			},
			verify: func(t *testing.T, got map[string]any) {
				assert.NotEmpty(t, got)
				assert.LessOrEqual(t, len(got), maxFlattenFields)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.verify(t, flattenContext(tt.build()))
		})
	}
}
