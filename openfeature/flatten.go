// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

const (
	// maxContextDepth is the deepest value retained below the root context (depth 0).
	// Top-level attributes have depth 1; values at depth 32 are kept, at 33 skipped.
	maxContextDepth = 32

	// maxFlattenVisits bounds work on the caller's goroutine in exposureHook.Finally.
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
}

func (s *flattenState) enter(ptr uintptr, length int) (containerID, bool) {
	id := containerID{ptr: ptr, length: length}
	if _, cyclic := s.seen[id]; cyclic {
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
	if st.visits >= maxFlattenVisits {
		return
	}
	st.visits++
	if depth > maxContextDepth || len(prefix) > maxKeyLength {
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
		result[prefix] = string(v)
	case fmt.Stringer:
		// A typed nil pointer may have a value-receiver String method that panics.
		if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer && rv.IsNil() {
			return
		}
		result[prefix] = v.String()
	case string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, bool:
		result[prefix] = value
	default:
		logOnboarding("skipping unsupported attribute type for key %q: %T", prefix, value)
	}
}

func flattenRecursiveMap[T any](prefix string, v map[string]T, result map[string]any, st *flattenState, depth int) {
	for key, val := range v {
		if st.visits >= maxFlattenVisits {
			return
		}
		// Reject before concatenation; even an oversized input key must not
		// cause an oversized allocation. Skipped children still cost a visit.
		keyLength := len(key)
		if prefix != "" {
			keyLength += len(prefix) + 1
		}
		if keyLength > maxKeyLength {
			st.visits++
			continue
		}
		newPrefix := key
		if prefix != "" {
			newPrefix = prefix + "." + key
		}
		flattenRecursiveDepth(newPrefix, val, result, st, depth+1)
	}
}

func flattenRecursiveArray[T any](prefix string, v []T, result map[string]any, st *flattenState, depth int) {
	for i, item := range v {
		if st.visits >= maxFlattenVisits {
			return
		}
		flattenKey := prefix + "." + strconv.Itoa(i)
		flattenRecursiveDepth(flattenKey, item, result, st, depth+1)
	}
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
