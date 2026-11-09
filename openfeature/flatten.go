// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

const (
	// maxContextDepth is the deepest value retained below the root context (depth 0).
	// Top-level attributes have depth 1; values at depth 32 are kept, at 33 skipped.
	maxContextDepth = 32

	// maxFlattenVisits bounds traversal on the caller's goroutine for both telemetry paths.
	// Count all values, including containers and leaves that emit no field, so shared
	// subtrees cannot cause exponential traversal even when the result stays empty.
	maxFlattenVisits = 1 << 12
)

// containerID distinguishes prefix subslices sharing a data pointer from cycles.
type containerID struct {
	ptr    uintptr
	length int
}

type flattenState struct {
	seen   map[containerID]struct{} // only containers on the current recursion path
	visits int
	prune  bool // evaluation-count limits; exposure attributes keep their existing scalar contract
	mask   truncReasonMask
}

func (s *flattenState) exhausted(result map[string]any) bool {
	if s.visits >= maxFlattenVisits {
		s.mask.add(bitMaxVisitedNodes)
		return true
	}
	if s.prune && len(result) >= maxContextFields {
		s.mask.add(bitMaxContextFields)
		return true
	}
	return false
}

func (s *flattenState) containerDepth(depth int) bool {
	if s.prune && depth >= maxSnapshotDepth {
		s.mask.add(bitMaxSnapshotDepth)
		return false
	}
	return true
}

func (s *flattenState) put(result map[string]any, key string, value any) {
	if v, ok := value.(string); s.prune && ok && len(v) > maxFieldLength {
		s.mask.add(bitMaxValueLength)
		return
	}
	result[key] = value
}

// Check before concatenating or sorting: even skipped caller keys can be arbitrarily long.
func (s *flattenState) keyFits(prefix, key string, depth int) bool {
	n := len(key)
	if prefix != "" || (s.prune && depth >= 0) {
		n += len(prefix) + 1
	}
	if n > maxKeyLength {
		s.visits++
		s.mask.add(bitMaxKeyLength)
		return false
	}
	return true
}

func (s *flattenState) mapKey(prefix, key string, depth int) string {
	if prefix == "" && !(s.prune && depth >= 0) {
		return key
	}
	return prefix + "." + key
}

// A bounded sample avoids scanning or sorting the whole input map. Within the width
// cap, sorted traversal preserves stable aggregation keys. Above it, the subset can vary.
func (s *flattenState) mapLimit(size, depth int) int {
	limit := maxStructureProperties
	reason := bitMaxStructureProperties
	if depth < 0 {
		limit = maxContextFields
		reason = bitMaxContextFields
	}
	if size > limit {
		s.mask.add(reason)
	}
	return min(size, limit)
}

// Only the bounded prefix participates in comparison, so sorting cannot scan
// arbitrarily long caller keys. Keys beyond the wire limit are skipped on traversal.
func sortFlattenKeys(keys []string) {
	slices.SortFunc(keys, func(a, b string) int {
		return strings.Compare(a[:min(len(a), maxKeyLength+1)], b[:min(len(b), maxKeyLength+1)])
	})
}

func (s *flattenState) listLimit(size int) int {
	if s.prune && size > maxListElements {
		s.mask.add(bitMaxListElements)
		return maxListElements
	}
	return size
}

func (s *flattenState) enter(ptr uintptr, length int) (containerID, bool) {
	id := containerID{ptr: ptr, length: length}
	if _, cyclic := s.seen[id]; cyclic {
		s.mask.add(bitCycle)
		return id, false
	}
	s.seen[id] = struct{}{}
	return id, true
}

// flattenRecursive recursively flattens nested attributes into a single-level map
// using dot notation for nested keys. This ensures that subject attributes in exposure
// events comply with the EVP intake schema which does not allow nested objects.
//
// For example:
//
//	{"user": {"id": "123", "email": "test@example.com"}}
//
// becomes:
//
//	{"user.id": "123", "user.email": "test@example.com"}
//
// It skips values deeper than maxContextDepth, keys longer than maxKeyLength, and
// cycles through maps/slices. Each traversal stops after maxFlattenVisits values.
// These limits bound traversal and key construction, not scalar value sizes or
// arbitrary user-defined String methods.
func flattenRecursive(prefix string, value any, result map[string]any) {
	flattenRecursiveDepth(prefix, value, result, &flattenState{seen: make(map[containerID]struct{}, 1)}, 0)
}

func flattenRecursiveDepth(prefix string, value any, result map[string]any, st *flattenState, depth int) {
	if st.exhausted(result) {
		return
	}
	st.visits++
	if depth > maxContextDepth {
		st.mask.add(bitMaxSnapshotDepth)
		return
	}
	if len(prefix) > maxKeyLength {
		st.mask.add(bitMaxKeyLength)
		return
	}

	switch v := value.(type) {
	case map[string]any:
		id, ok := st.enter(reflect.ValueOf(v).Pointer(), len(v))
		if !ok {
			return
		}
		flattenRecursiveMap(prefix, v, result, st, depth)
		delete(st.seen, id)
	case map[string]string:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]uint:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]int:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]int64:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]int32:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]uint64:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]uint32:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]int16:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]int8:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]uint16:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]uint8:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]float64:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]bool:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case map[string]float32:
		flattenRecursiveMap(prefix, v, result, st, depth)
	case []any:
		id, ok := st.enter(reflect.ValueOf(v).Pointer(), len(v))
		if !ok {
			return
		}
		flattenRecursiveArray(prefix, v, result, st, depth)
		delete(st.seen, id)
	case []string:
		flattenRecursiveArray(prefix, v, result, st, depth)
	case []int:
		flattenRecursiveArray(prefix, v, result, st, depth)
	case []int64:
		flattenRecursiveArray(prefix, v, result, st, depth)
	case []int32:
		flattenRecursiveArray(prefix, v, result, st, depth)
	case []uint64:
		flattenRecursiveArray(prefix, v, result, st, depth)
	case []uint32:
		flattenRecursiveArray(prefix, v, result, st, depth)
	case []int16:
		flattenRecursiveArray(prefix, v, result, st, depth)
	case []uint16:
		flattenRecursiveArray(prefix, v, result, st, depth)
	case []float64:
		flattenRecursiveArray(prefix, v, result, st, depth)
	case []bool:
		flattenRecursiveArray(prefix, v, result, st, depth)
	case []float32:
		flattenRecursiveArray(prefix, v, result, st, depth)
	case []byte:
		if st.prune && len(v) > maxFieldLength {
			st.mask.add(bitMaxValueLength)
			return
		}
		st.put(result, prefix, string(v))
	case fmt.Stringer:
		// A typed nil pointer may have a value-receiver String method that panics.
		if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
			return
		}
		st.put(result, prefix, v.String())
	case string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, bool:
		st.put(result, prefix, value)
	case nil:
		if st.prune {
			result[prefix] = nil
		}
	default:
		if st.prune && flattenReflect(prefix, value, result, st, depth) {
			return
		}
		logOnboarding("skipping unsupported attribute type for key %q: %T", prefix, value)
	}
}

func flattenRecursiveMap[T any](prefix string, v map[string]T, result map[string]any, st *flattenState, depth int) {
	if !st.containerDepth(depth) {
		return
	}
	if st.prune {
		limit := st.mapLimit(len(v), depth)
		keys := make([]string, 0, limit)
		scanned := 0
		for key := range v {
			if scanned >= limit {
				break
			}
			scanned++
			keys = append(keys, key)
		}
		sortFlattenKeys(keys)
		for _, key := range keys {
			if st.exhausted(result) {
				return
			}
			if !st.keyFits(prefix, key, depth) {
				continue
			}
			flattenRecursiveDepth(st.mapKey(prefix, key, depth), v[key], result, st, depth+1)
		}
		if scanned < len(v) && st.visits >= maxFlattenVisits {
			st.mask.add(bitMaxVisitedNodes)
		}
		return
	}
	for key, val := range v {
		if st.exhausted(result) {
			return
		}
		if !st.keyFits(prefix, key, depth) {
			continue
		}
		flattenRecursiveDepth(st.mapKey(prefix, key, depth), val, result, st, depth+1)
	}
}

func flattenRecursiveArray[T any](prefix string, v []T, result map[string]any, st *flattenState, depth int) {
	if !st.containerDepth(depth) {
		return
	}
	limit := st.listLimit(len(v))
	for i := range limit {
		if st.exhausted(result) {
			return
		}
		flattenRecursiveDepth(prefix+"."+strconv.Itoa(i), v[i], result, st, depth+1)
	}
}

// Reflection preserves support for named/typed evaluation-context containers without
// allocating a full MapKeys slice. Arrays have no identity and must not call Pointer.
func flattenReflect(prefix string, value any, result map[string]any, st *flattenState, depth int) bool {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return false
		}
		if !st.containerDepth(depth) {
			return true
		}
		id, ok := st.enter(rv.Pointer(), rv.Len())
		if !ok {
			return true
		}
		defer delete(st.seen, id)
		limit := st.mapLimit(rv.Len(), depth)
		keys := make([]string, 0, limit)
		iter := rv.MapRange()
		scanned := 0
		for scanned < limit && iter.Next() {
			scanned++
			key := iter.Key().String()
			keys = append(keys, key)
		}
		sortFlattenKeys(keys)
		for _, key := range keys {
			if st.exhausted(result) {
				return true
			}
			if !st.keyFits(prefix, key, depth) {
				continue
			}
			mapKey := reflect.ValueOf(key).Convert(rv.Type().Key())
			flattenRecursiveDepth(st.mapKey(prefix, key, depth), rv.MapIndex(mapKey).Interface(), result, st, depth+1)
		}
		if scanned < rv.Len() && st.visits >= maxFlattenVisits {
			st.mask.add(bitMaxVisitedNodes)
		}
		return true
	case reflect.Slice, reflect.Array:
		if !st.containerDepth(depth) {
			return true
		}
		if rv.Kind() == reflect.Slice {
			id, ok := st.enter(rv.Pointer(), rv.Len())
			if !ok {
				return true
			}
			defer delete(st.seen, id)
		}
		limit := st.listLimit(rv.Len())
		for i := range limit {
			if st.exhausted(result) {
				return true
			}
			flattenRecursiveDepth(prefix+"."+strconv.Itoa(i), rv.Index(i).Interface(), result, st, depth+1)
		}
		return true
	}
	return false
}

// flattenContext flattens the OpenFeature evaluation context attributes.
// It converts nested attributes to dot notation for exposure event reporting.
func flattenContext(context map[string]any) map[string]any {
	if context == nil {
		return make(map[string]any)
	}

	flattened := make(map[string]any)

	// Flatten each top-level value in the context
	flattenRecursive("", context, flattened)

	if len(flattened) == 0 {
		return nil
	}

	return flattened
}

// extractPrimitiveAttributes extracts only primitive-type attributes from a flattened map.
// This is used for exposure event subject attributes to ensure only valid types are sent.
// Valid primitive types are: string, int, int64, float64, bool
func extractPrimitiveAttributes(attributes map[string]any) map[string]any {
	if attributes == nil {
		return nil
	}

	result := make(map[string]any)
	for key, value := range attributes {
		// Skip the targeting key as it's sent separately as subject.id
		if strings.HasPrefix(key, "targetingKey") {
			continue
		}

		// Only include primitive types
		switch value.(type) {
		case string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, bool:
			result[key] = value
		default:
			// Skip non-primitive types (e.g., slices, maps that weren't flattened)
			continue
		}
	}

	if len(result) == 0 {
		return nil
	}

	return result
}
