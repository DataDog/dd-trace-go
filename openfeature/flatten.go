// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
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
func flattenRecursive(prefix string, value any, result map[string]any) {
	switch v := value.(type) {
	case map[string]any:
		flattenRecursiveMap(prefix, v, result)
	case map[string]string:
		flattenRecursiveMap(prefix, v, result)
	case map[string]uint:
		flattenRecursiveMap(prefix, v, result)
	case map[string]int:
		flattenRecursiveMap(prefix, v, result)
	case map[string]int64:
		flattenRecursiveMap(prefix, v, result)
	case map[string]int32:
		flattenRecursiveMap(prefix, v, result)
	case map[string]uint64:
		flattenRecursiveMap(prefix, v, result)
	case map[string]uint32:
		flattenRecursiveMap(prefix, v, result)
	case map[string]int16:
		flattenRecursiveMap(prefix, v, result)
	case map[string]int8:
		flattenRecursiveMap(prefix, v, result)
	case map[string]uint16:
		flattenRecursiveMap(prefix, v, result)
	case map[string]uint8:
		flattenRecursiveMap(prefix, v, result)
	case map[string]float64:
		flattenRecursiveMap(prefix, v, result)
	case map[string]bool:
		flattenRecursiveMap(prefix, v, result)
	case map[string]float32:
		flattenRecursiveMap(prefix, v, result)
	case []string:
		flattenRecursiveArray(prefix, v, result)
	case []int:
		flattenRecursiveArray(prefix, v, result)
	case []int64:
		flattenRecursiveArray(prefix, v, result)
	case []int32:
		flattenRecursiveArray(prefix, v, result)
	case []uint64:
		flattenRecursiveArray(prefix, v, result)
	case []uint32:
		flattenRecursiveArray(prefix, v, result)
	case []int16:
		flattenRecursiveArray(prefix, v, result)
	case []uint16:
		flattenRecursiveArray(prefix, v, result)
	case []float64:
		flattenRecursiveArray(prefix, v, result)
	case []bool:
		flattenRecursiveArray(prefix, v, result)
	case []float32:
		flattenRecursiveArray(prefix, v, result)
	case []any:
		flattenRecursiveArray(prefix, v, result)
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

func flattenRecursiveMap[T any](prefix string, v map[string]T, result map[string]any) {
	for key, val := range v {
		newPrefix := key
		if prefix != "" {
			newPrefix = prefix + "." + key
		}
		flattenRecursive(newPrefix, val, result)
	}
}

func flattenRecursiveArray[T any](prefix string, v []T, result map[string]any) {
	for i, item := range v {
		flattenKey := prefix + "." + strconv.Itoa(i)
		flattenRecursive(flattenKey, item, result)
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
