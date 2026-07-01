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

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// maxContextDepth bounds the recursion depth of context flattening.
//
// The evaluation context is attacker-influenceable: a deeply nested or self-referential
// map[string]any / []any would otherwise recurse without limit. In Go a stack overflow is a
// fatal runtime error ("goroutine stack exceeds ... limit") that cannot be caught by recover(),
// so an unbounded traversal is a process-crash DoS rather than a mere degraded telemetry path.
// 32 is far deeper than any legitimate evaluation context and matches the other SDKs
// (see dd-trace-rb Aggregator::MAX_CONTEXT_DEPTH).
const maxContextDepth = 32

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
// The flattening is applied during both flag evaluation and exposure event creation.
//
// Traversal is bounded: recursion stops at maxContextDepth and cycles through
// map[string]any / []any containers are detected and skipped so an attacker-supplied
// deeply nested or self-referential context cannot exhaust the stack.
func flattenRecursive(prefix string, value any, result map[string]any) {
	flattenValue(prefix, value, result, nil, 0)
}

// flattenValue is the depth- and cycle-bounded core of flattenRecursive.
//
// seen tracks the identity of map[string]any / []any containers currently on the recursion
// stack (they are the only element types that can form a cycle, since their values are `any`).
// It is allocated lazily on the first such container and threaded through the recursion; a
// container is added before descending and removed on the way out, so shared sub-trees (a
// diamond) are still fully flattened while genuine cycles are broken.
func flattenValue(prefix string, value any, result map[string]any, seen map[uintptr]struct{}, depth int) {
	if depth > maxContextDepth {
		log.Debug("openfeature: skipping attribute %q: context nesting exceeds max depth %d", prefix, maxContextDepth)
		return
	}

	switch v := value.(type) {
	case map[string]any:
		if seen = enterContainer(seen, v, prefix); seen == nil {
			return
		}
		flattenRecursiveMap(prefix, v, result, seen, depth)
		delete(seen, reflect.ValueOf(v).Pointer())
	case map[string]string:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]uint:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]int:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]int64:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]int32:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]uint64:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]uint32:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]int16:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]int8:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]uint16:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]uint8:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]float64:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]bool:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case map[string]float32:
		flattenRecursiveMap(prefix, v, result, seen, depth)
	case []any:
		if seen = enterContainer(seen, v, prefix); seen == nil {
			return
		}
		flattenRecursiveArray(prefix, v, result, seen, depth)
		delete(seen, reflect.ValueOf(v).Pointer())
	case []string:
		flattenRecursiveArray(prefix, v, result, seen, depth)
	case []int:
		flattenRecursiveArray(prefix, v, result, seen, depth)
	case []int64:
		flattenRecursiveArray(prefix, v, result, seen, depth)
	case []int32:
		flattenRecursiveArray(prefix, v, result, seen, depth)
	case []uint64:
		flattenRecursiveArray(prefix, v, result, seen, depth)
	case []uint32:
		flattenRecursiveArray(prefix, v, result, seen, depth)
	case []int16:
		flattenRecursiveArray(prefix, v, result, seen, depth)
	case []uint16:
		flattenRecursiveArray(prefix, v, result, seen, depth)
	case []float64:
		flattenRecursiveArray(prefix, v, result, seen, depth)
	case []bool:
		flattenRecursiveArray(prefix, v, result, seen, depth)
	case []float32:
		flattenRecursiveArray(prefix, v, result, seen, depth)
	case []byte:
		result[prefix] = string(v)
	case fmt.Stringer:
		result[prefix] = v.String()
	case string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, bool:
		result[prefix] = value
	default:
		log.Debug("openfeature: skipping unsupported attribute type for key %q: %T", prefix, value)
	}
}

// enterContainer records the identity of a map[string]any / []any container before descending
// into it so a self-referential context is not traversed forever. It returns the (possibly
// newly-allocated) seen set to use for the descent, or nil if the container is already on the
// current recursion stack (a cycle), in which case the caller must skip it.
func enterContainer(seen map[uintptr]struct{}, container any, prefix string) map[uintptr]struct{} {
	ptr := reflect.ValueOf(container).Pointer()
	if _, ok := seen[ptr]; ok {
		log.Debug("openfeature: skipping attribute %q: cyclic evaluation context reference", prefix)
		return nil
	}
	if seen == nil {
		seen = make(map[uintptr]struct{}, 1)
	}
	seen[ptr] = struct{}{}
	return seen
}

func flattenRecursiveMap[T any](prefix string, v map[string]T, result map[string]any, seen map[uintptr]struct{}, depth int) {
	for key, val := range v {
		newPrefix := key
		if prefix != "" {
			newPrefix = prefix + "." + key
		}
		flattenValue(newPrefix, val, result, seen, depth+1)
	}
}

func flattenRecursiveArray[T any](prefix string, v []T, result map[string]any, seen map[uintptr]struct{}, depth int) {
	for i, item := range v {
		flattenKey := prefix + "." + strconv.Itoa(i)
		flattenValue(flattenKey, item, result, seen, depth+1)
	}
}

// flattenContext flattens the OpenFeature evaluation context attributes.
// It ensures that nested attributes are converted to dot notation for both
// evaluation purposes and exposure event reporting.
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
