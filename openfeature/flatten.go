// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"fmt"
	"strings"
)

// flattenAttributes recursively flattens nested attributes into a single-level map
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
func flattenAttributes(attributes map[string]any) map[string]any {
	if attributes == nil {
		return nil
	}

	result := make(map[string]any)
	flattenRecursive("", attributes, result)
	return result
}

// flattenRecursive is the recursive helper function that traverses the attribute tree
// and builds the flattened map with dot-notation keys.
func flattenRecursive(prefix string, value any, result map[string]any) {
	switch v := value.(type) {
	case map[string]any:
		// Recursively flatten nested maps
		for key, val := range v {
			newPrefix := key
			if prefix != "" {
				newPrefix = prefix + "." + key
			}
			flattenRecursive(newPrefix, val, result)
		}
	default:
		// For non-map values, add them directly to the result
		if prefix != "" {
			result[prefix] = value
		}
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
	for key, value := range context {
		switch v := value.(type) {
		case map[string]any:
			// If the value is a nested map, flatten it with the key as prefix
			for nestedKey, nestedValue := range flattenAttributes(v) {
				flattenKey := key + "." + nestedKey
				flattened[flattenKey] = nestedValue
			}
		default:
			// For non-map values, keep them as-is
			flattened[key] = value
		}
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

// convertToString attempts to convert a value to a string representation.
// This is primarily used for ensuring consistent key formatting.
func convertToString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}
