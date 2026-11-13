// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFlattenContext covers ordinary exposure attributes, cycles, and traversal limits.
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
				assert.Empty(t, got)
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
				assert.Empty(t, got)
			},
		},
		{
			name: "shared-subtree fan-out is bounded by the visit budget",
			build: func() map[string]any {
				// Branching-2 nesting reusing the same child at each level would expand to ~2^20
				// leaves without the ceiling; depth (20) stays under maxContextDepth so the depth
				// cap alone does not save us here — only maxFlattenVisits does.
				var cur any = map[string]any{"x": "v"}
				for range 20 {
					cur = map[string]any{"a": cur, "b": cur}
				}
				return map[string]any{"root": cur}
			},
			verify: func(t *testing.T, got map[string]any) {
				assert.NotEmpty(t, got)
				assert.LessOrEqual(t, len(got), maxFlattenVisits)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.verify(t, flattenContext(tt.build()))
		})
	}
}

func TestFlattenDepthBoundary(t *testing.T) {
	for _, array := range []bool{false, true} {
		for _, depth := range []int{maxContextDepth - 1, maxContextDepth, maxContextDepth + 1} {
			t.Run(fmt.Sprintf("array=%t/depth=%d", array, depth), func(t *testing.T) {
				var value any = "leaf"
				suffix := ".n"
				if array {
					suffix = ".0"
				}
				key := "root" + strings.Repeat(suffix, depth-1)
				for range depth - 1 {
					if array {
						value = []any{value}
					} else {
						value = map[string]any{"n": value}
					}
				}
				want := map[string]any{"shallow": "kept"}
				if depth <= maxContextDepth {
					want[key] = "leaf"
				}
				assert.Equal(t, want, flattenContext(map[string]any{"root": value, "shallow": "kept"}))
			})
		}
	}
}

func TestFlattenSliceIdentity(t *testing.T) {
	self := []any{"a", nil}
	self[1] = self
	a := []any{"leaf", nil}
	b := []any{a}
	a[1] = b
	shared := []any{"v"}
	prefix := []any{"leaf", nil}
	prefix[1] = prefix[:1]
	for _, tt := range []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{"self", map[string]any{"list": self}, map[string]any{"list.0": "a"}},
		{"indirect", map[string]any{"root": a}, map[string]any{"root.0": "leaf"}},
		{"diamond", map[string]any{"x": shared, "y": shared}, map[string]any{"x.0": "v", "y.0": "v"}},
		{"prefix", map[string]any{"arr": prefix}, map[string]any{"arr.0": "leaf", "arr.1.0": "leaf"}},
	} {
		t.Run(tt.name, func(t *testing.T) { assert.Equal(t, tt.want, flattenContext(tt.in)) })
	}
}

func TestFlattenVisitBudget(t *testing.T) {
	for _, leaf := range []any{nil, map[string]any{}, []any{}, struct{}{}, "kept"} {
		t.Run(fmt.Sprintf("%T", leaf), func(t *testing.T) {
			var cur any = leaf
			for range 16 {
				cur = map[string]any{"a": cur, "b": cur}
			}
			st := &flattenState{seen: make(map[containerID]struct{})}
			result := make(map[string]any)
			flattenRecursiveDepth("", cur, result, st, 0)
			assert.Equal(t, maxFlattenVisits, st.visits)
			assert.LessOrEqual(t, len(result), maxFlattenVisits)
			assert.Empty(t, st.seen)
		})
	}
	for _, size := range []int{2048, 2 * maxFlattenVisits} {
		t.Run(fmt.Sprintf("wide=%d", size), func(t *testing.T) {
			in := make(map[string]any, size)
			for i := range size {
				in[strconv.Itoa(i)] = i
			}
			got := flattenContext(in)
			if size < maxFlattenVisits {
				assert.Equal(t, in, got)
			} else {
				assert.Len(t, got, maxFlattenVisits-1)
			}
		})
	}
	t.Run("array", func(t *testing.T) {
		st := &flattenState{seen: make(map[containerID]struct{})}
		flattenRecursiveDepth("", make([]any, 2*maxFlattenVisits), make(map[string]any), st, 0)
		assert.Equal(t, maxFlattenVisits, st.visits)
	})
}

func TestFlattenKeyBoundary(t *testing.T) {
	for _, size := range []int{maxKeyLength - 1, maxKeyLength, maxKeyLength + 1} {
		key := strings.Repeat("k", size)
		got := flattenContext(map[string]any{key: "value"})
		if size <= maxKeyLength {
			assert.Equal(t, map[string]any{key: "value"}, got)
		} else {
			assert.Empty(t, got)
		}
	}
	for _, size := range []int{maxKeyLength - 2, maxKeyLength - 1} {
		key := strings.Repeat("k", size)
		got := flattenContext(map[string]any{key: []any{"value"}})
		if size+2 <= maxKeyLength {
			assert.Equal(t, map[string]any{key + ".0": "value"}, got)
		} else {
			assert.Empty(t, got)
		}
	}

	key := strings.Repeat("k", maxKeyLength-2)
	assert.Equal(t, map[string]any{key + ".x": "keep"}, flattenContext(map[string]any{key: map[string]any{"x": "keep", "xx": "drop"}}))
	st := &flattenState{seen: make(map[containerID]struct{})}
	wide := make(map[string]any, 2*maxFlattenVisits)
	for i := range 2 * maxFlattenVisits {
		wide[strings.Repeat("x", maxKeyLength)+strconv.Itoa(i)] = nil
	}
	flattenRecursiveDepth("", wide, make(map[string]any), st, 0)
	assert.Equal(t, maxFlattenVisits, st.visits)
}

func TestFlattenNilStringer(t *testing.T) {
	assert.Equal(t, map[string]any{"plan": "pro"}, flattenContext(map[string]any{"lastLogin": (*time.Time)(nil), "plan": "pro"}))
}

func BenchmarkFlattenExposureContext(b *testing.B) {
	normal := map[string]any{"user": map[string]any{"id": "123", "plan": "pro"}, "tags": []any{"a", "b"}}
	var shared any = nil
	for range 22 {
		shared = map[string]any{"a": shared, "b": shared}
	}
	for _, tt := range []struct {
		name string
		in   map[string]any
	}{{"normal", normal}, {"empty_shared", shared.(map[string]any)}} {
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				flattenContext(tt.in)
			}
		})
	}
}

func TestExposureHookFlattenBounds(t *testing.T) {
	cyclic := map[string]any{"leaf": "kept"}
	cyclic["self"] = cyclic
	var emptyDAG any = nil
	for range 20 {
		emptyDAG = map[string]any{"a": emptyDAG, "b": emptyDAG}
	}
	for _, tt := range []struct {
		name  string
		attrs map[string]any
		want  map[string]any
	}{
		{"cycle", map[string]any{"root": cyclic}, map[string]any{"root.leaf": "kept"}},
		{"empty shared subtree", map[string]any{"root": emptyDAG}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			writer := &exposureWriter{datadogLoggingEnabled: true, buffer: make([]exposureEvent, 0, 1), cache: newExposureLRUCache(10)}
			hook := newExposureHook(writer)
			hc := of.NewHookContext("flag", of.Boolean, false, of.ClientMetadata{}, of.Metadata{Name: datadogProviderName}, of.NewEvaluationContext("subject", tt.attrs))
			details := of.InterfaceEvaluationDetails{Value: true, EvaluationDetails: of.EvaluationDetails{FlagKey: "flag", FlagType: of.Boolean, ResolutionDetail: of.ResolutionDetail{Variant: "v", FlagMetadata: of.FlagMetadata{metadataAllocationKey: "allocation", metadataDoLogKey: true}}}}
			hook.Finally(context.Background(), hc, details, of.HookHints{})
			require.Len(t, writer.buffer, 1)
			assert.Equal(t, "subject", writer.buffer[0].Subject.ID)
			assert.Equal(t, tt.want, writer.buffer[0].Subject.Attributes)
		})
	}
}

// Once the budget is exhausted, the array loop must stop constructing keys,
// not merely rely on the recursive entry guard to discard later elements.
func TestFlattenArrayBudgetStopsIteration(t *testing.T) {
	small := make([]any, 2*maxFlattenVisits)
	large := make([]any, 16*maxFlattenVisits)
	allocations := func(values []any) float64 {
		return testing.AllocsPerRun(3, func() { flattenContext(map[string]any{"values": values}) })
	}
	assert.LessOrEqual(t, allocations(large), allocations(small)+10)
}
