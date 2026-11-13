// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	of "github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sharedEmptyContext() map[string]any {
	var node any = map[string]any{}
	for range maxSnapshotDepth {
		m := make(map[string]any, 32)
		for i := range 32 {
			m[strconv.Itoa(i)] = node
		}
		node = m
	}
	return map[string]any{"root": node}
}

func TestSharedFlattenVisitBudget(t *testing.T) {
	for _, prune := range []bool{false, true} {
		t.Run(fmt.Sprintf("evaluation=%t", prune), func(t *testing.T) {
			st := &flattenState{seen: make(map[containerID]struct{}), prune: prune}
			depth := 0
			if prune {
				depth = -1
			}
			out := make(map[string]any)
			flattenRecursiveDepth("", sharedEmptyContext(), out, st, depth)
			assert.Empty(t, out)
			assert.Equal(t, maxFlattenVisits, st.visits)
			assert.Contains(t, st.mask.reasons(), truncReasonMaxVisitedNodes)
			assert.Empty(t, st.seen)
		})
	}
	_, reasons := flattenAndPruneContext(sharedEmptyContext())
	assert.Contains(t, reasons, truncReasonMaxVisitedNodes)
}

func TestEvaluationFlattenWideSubset(t *testing.T) {
	for _, size := range []int{256, 257, 5000} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			attrs := make(map[string]any, size)
			for i := range size {
				attrs[strconv.Itoa(i)] = i
			}
			for range 10 {
				out, reasons := flattenAndPruneContext(attrs)
				require.Len(t, out, maxContextFields)
				for k, v := range out {
					assert.Equal(t, attrs[k], v)
				}
				if size > maxContextFields {
					assert.Contains(t, reasons, truncReasonMaxContextFields)
				} else {
					assert.Empty(t, reasons)
				}
			}
		})
	}
}

func TestEvaluationFlattenContainerIdentity(t *testing.T) {
	type namedSlice []any
	type namedMap map[string]any
	type namedKey string
	prefix := []any{"leaf", nil}
	prefix[1] = prefix[:1]
	cycle := namedSlice{"keep", nil}
	cycle[1] = cycle
	m := namedMap{"keep": true}
	m["cycle"] = m
	shared := []any{"shared"}
	for _, tc := range []struct {
		name   string
		input  map[string]any
		want   map[string]any
		cyclic bool
	}{
		{"prefix", map[string]any{"a": prefix}, map[string]any{"a.0": "leaf", "a.1.0": "leaf"}, false},
		{"diamond", map[string]any{"a": shared, "b": shared}, map[string]any{"a.0": "shared", "b.0": "shared"}, false},
		{"named slice cycle", map[string]any{"a": cycle}, map[string]any{"a.0": "keep"}, true},
		{"named map cycle", map[string]any{"a": m}, map[string]any{"a.keep": true}, true},
		{"array", map[string]any{"a": [2]string{"x", "y"}}, map[string]any{"a.0": "x", "a.1": "y"}, false},
		{"named keys", map[string]any{"a": map[namedKey]int{"x": 1}}, map[string]any{"a.x": 1}, false},
		{"unsupported keys", map[string]any{"a": map[int]string{1: "x"}, "keep": true}, map[string]any{"keep": true}, false},
		{"empty key", map[string]any{"": map[string]any{"x": 1}}, map[string]any{".x": 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, reasons := flattenAndPruneContext(tc.input)
			assert.Equal(t, tc.want, out)
			if tc.cyclic {
				assert.Equal(t, []string{truncReasonCycle}, reasons)
			} else {
				assert.Empty(t, reasons)
			}
		})
	}
}

func TestEvaluationFlattenTypedBounds(t *testing.T) {
	type namedMap map[string]int
	for _, reflectMap := range []bool{false, true} {
		t.Run(fmt.Sprintf("reflect=%t", reflectMap), func(t *testing.T) {
			m := make(map[string]int, 5000)
			for i := range 5000 {
				m[strconv.Itoa(i)] = i
			}
			var value any = m
			if reflectMap {
				value = namedMap(m)
			}
			out, reasons := flattenAndPruneContext(map[string]any{"m": value})
			assert.Len(t, out, maxStructureProperties)
			assert.Contains(t, reasons, truncReasonMaxStructureProperties)
		})
	}
	// Oversized keys must be skipped before concatenation, sorting or reflective lookup.
	for _, value := range []any{map[string]int{strings.Repeat("x", 1<<20): 1}, namedMap{strings.Repeat("x", 1<<20): 1}} {
		out, reasons := flattenAndPruneContext(map[string]any{"m": value, "keep": true})
		assert.Equal(t, map[string]any{"keep": true}, out)
		assert.Equal(t, []string{truncReasonMaxKeyLength}, reasons)
	}
}

func TestRecordSharedVisitBudget(t *testing.T) {
	hookCtx := of.NewHookContext("test-flag", of.Boolean, false, of.NewClientMetadata(""), of.Metadata{Name: "test"}, of.NewEvaluationContext("user", sharedEmptyContext()))
	for _, consent := range []bool{false, true} {
		t.Run(fmt.Sprintf("consent=%t", consent), func(t *testing.T) {
			w := newFlagEvalLoggingWriter(ProviderConfig{})
			details := of.InterfaceEvaluationDetails{Value: true, EvaluationDetails: of.EvaluationDetails{FlagKey: "test-flag", FlagType: of.Boolean, ResolutionDetail: of.ResolutionDetail{Variant: "on", Reason: of.TargetingMatchReason, FlagMetadata: of.FlagMetadata{metadataObserveFullEvaluationDataKey: consent}}}}
			w.record(hookCtx, details)
			require.Len(t, w.events, 1)
			ev := <-w.events
			assert.Empty(t, ev.contextAttrs)
			count, ok := w.contextTruncatedByReason.Load(truncReasonMaxVisitedNodes)
			if consent {
				require.True(t, ok)
				assert.Equal(t, int64(1), count.(*atomic.Int64).Load())
			} else {
				assert.False(t, ok)
			}
		})
	}
}

func BenchmarkSharedFlatten(b *testing.B) {
	normal := map[string]any{"user": map[string]any{"id": "123", "plan": "pro"}, "tags": []any{"a", "b"}}
	wide := make(map[string]any, 100000)
	for i := range 100000 {
		wide[strconv.Itoa(i)] = i
	}
	for name, input := range map[string]map[string]any{"normal": normal, "leaf-free": sharedEmptyContext(), "wide": wide} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				flattenAndPruneContext(input)
			}
		})
	}
}

func TestEvaluationFlattenStableNearVisitLimit(t *testing.T) {
	type namedMap map[string]any
	for _, value := range []any{map[string]any{"c": 3, "a": 1, "b": 2}, namedMap{"c": 3, "a": 1, "b": 2}} {
		for range 100 {
			st := &flattenState{seen: make(map[containerID]struct{}), prune: true, visits: maxFlattenVisits - 3}
			out := make(map[string]any)
			flattenRecursiveDepth("root", value, out, st, 0)
			assert.Equal(t, map[string]any{"root.a": 1, "root.b": 2}, out)
			assert.Equal(t, maxFlattenVisits, st.visits)
			assert.Equal(t, []string{truncReasonMaxVisitedNodes}, st.mask.reasons())
		}
	}
	// Rejected keys consume the same budget, including during reflective traversal.
	keys := map[string]any{strings.Repeat("x", maxKeyLength+1): 1, strings.Repeat("y", maxKeyLength+1): 2}
	for _, value := range []any{keys, namedMap(keys)} {
		st := &flattenState{seen: make(map[containerID]struct{}), prune: true, visits: maxFlattenVisits - 2}
		out := make(map[string]any)
		flattenRecursiveDepth("root", value, out, st, 0)
		assert.Empty(t, out)
		assert.Equal(t, maxFlattenVisits, st.visits)
		assert.Equal(t, []string{truncReasonMaxKeyLength, truncReasonMaxVisitedNodes}, st.mask.reasons())
	}
}
