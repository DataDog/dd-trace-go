// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import "maps"

const ciVisibilityMetaValueMaxChars = 5000

func truncateCIVisibilityMetaValue(v string) string {
	if len(v) <= ciVisibilityMetaValueMaxChars {
		return v
	}
	count := 0
	for i := range v {
		if count == ciVisibilityMetaValueMaxChars {
			return v[:i]
		}
		count++
	}
	return v
}

func truncateCIVisibilityMetaValues(meta map[string]string) map[string]string {
	truncated, _ := truncateCIVisibilityMetaValuesChanged(meta)
	return truncated
}

func truncateCIVisibilityMetadata(metadata map[string]map[string]string) map[string]map[string]string {
	var truncated map[string]map[string]string
	for key, values := range metadata {
		truncatedValues, changed := truncateCIVisibilityMetaValuesChanged(values)
		if changed && truncated == nil {
			truncated = make(map[string]map[string]string, len(metadata))
			maps.Copy(truncated, metadata)
		}
		if truncated != nil {
			truncated[key] = truncatedValues
		}
	}
	if truncated != nil {
		return truncated
	}
	return metadata
}

func truncateCIVisibilityMetaValuesChanged(meta map[string]string) (map[string]string, bool) {
	var truncated map[string]string
	for key, value := range meta {
		truncatedValue := truncateCIVisibilityMetaValue(value)
		if truncatedValue != value && truncated == nil {
			truncated = make(map[string]string, len(meta))
			maps.Copy(truncated, meta)
		}
		if truncated != nil {
			truncated[key] = truncatedValue
		}
	}
	if truncated != nil {
		return truncated, true
	}
	return meta, false
}
