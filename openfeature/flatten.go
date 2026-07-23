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

const (
	// maxContextDepth bounds how deep context flattening will recurse.
	//
	// The evaluation context is attacker-influenceable: a deeply nested map[string]any / []any
	// would otherwise recurse without limit. In Go a stack overflow is a fatal runtime error that
	// recover() cannot catch, so an unbounded traversal is a process-crash DoS. 32 is far deeper
	// than any real evaluation context and matches the other SDKs.
	maxContextDepth = 32

	// maxFlattenFields is a safety ceiling on the TOTAL number of fields a single flatten produces.
	//
	// Depth capping and cycle detection stop unbounded depth and true cycles, but a context that
	// shares the same child map/slice across many keys (a DAG) still fans out multiplicatively —
	// e.g. branching-2 nesting expands ~2^depth leaves before any downstream cap applies. This
	// bounds that memory/CPU amplification.
	//
	// It sits far above maxContextFields (the 256-field intake prune), so it never affects a real
	// context: legitimate inputs flatten well below it and the deterministic 256-field prune that
	// builds the aggregation bucket key is unchanged; only pathological amplification is truncated.
	// Flattening runs on the background aggregation worker, not the evaluation hot path, so this
	// adds no per-evaluation cost.
	maxFlattenFields = 1 << 16 // 65536
)

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
// Traversal is bounded so an attacker-supplied context cannot exhaust the stack or memory:
// recursion stops at maxContextDepth, cycles through map[string]any / []any containers are
// detected and skipped, and the total field count is capped at maxFlattenFields.
func flattenRecursive(prefix string, value any, result map[string]any) {
	flattenRecursiveDepth(prefix, value, result, nil, 0)
}

func flattenRecursiveDepth(prefix string, value any, result map[string]any, seen map[uintptr]struct{}, depth int) {
	if len(result) >= maxFlattenFields {
		return
	}
	if depth > maxContextDepth {
		log.Debug("openfeature: skipping attribute %q: context nesting exceeds max depth %d", prefix, maxContextDepth)
		return
	}

	switch v := value.(type) {
	case map[string]any:
		// map[string]any values are `any`, so this container can reference itself (a cycle) or be
		// shared. Track its identity on the recursion stack and skip if we are already inside it.
		ptr := reflect.ValueOf(v).Pointer()
		if _, cyclic := seen[ptr]; cyclic {
			log.Debug("openfeature: skipping attribute %q: cyclic evaluation context reference", prefix)
			return
		}
		if seen == nil {
			seen = make(map[uintptr]struct{}, 1)
		}
		seen[ptr] = struct{}{}
		flattenRecursiveMap(prefix, v, result, seen, depth)
		delete(seen, ptr)
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
		// []any elements are `any` and can likewise reference the slice itself or be shared.
		ptr := reflect.ValueOf(v).Pointer()
		if _, cyclic := seen[ptr]; cyclic {
			log.Debug("openfeature: skipping attribute %q: cyclic evaluation context reference", prefix)
			return
		}
		if seen == nil {
			seen = make(map[uintptr]struct{}, 1)
		}
		seen[ptr] = struct{}{}
		flattenRecursiveArray(prefix, v, result, seen, depth)
		delete(seen, ptr)
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

func flattenRecursiveMap[T any](prefix string, v map[string]T, result map[string]any, seen map[uintptr]struct{}, depth int) {
	for key, val := range v {
		newPrefix := key
		if prefix != "" {
			newPrefix = prefix + "." + key
		}
		flattenRecursiveDepth(newPrefix, val, result, seen, depth+1)
	}
}

func flattenRecursiveArray[T any](prefix string, v []T, result map[string]any, seen map[uintptr]struct{}, depth int) {
	for i, item := range v {
		flattenKey := prefix + "." + strconv.Itoa(i)
		flattenRecursiveDepth(flattenKey, item, result, seen, depth+1)
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
