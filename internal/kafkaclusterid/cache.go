// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkaclusterid

import (
	"slices"
	"strings"
	"sync"
)

var cache sync.Map

// NormalizeBootstrapServers returns a canonical form of a comma-separated list
// of broker addresses. It trims whitespace, removes empty entries, and sorts
// entries lexicographically.
func NormalizeBootstrapServers(bootstrapServers string) string {
	var parts []string
	for s := range strings.SplitSeq(bootstrapServers, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			parts = append(parts, s)
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// NormalizeBootstrapServersList is like NormalizeBootstrapServers but accepts a
// slice of addresses instead of a comma-separated string.
func NormalizeBootstrapServersList(addrs []string) string {
	var parts []string
	for _, s := range addrs {
		s = strings.TrimSpace(s)
		if s != "" {
			parts = append(parts, s)
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// GetCachedID returns a cached cluster ID for the given bootstrap servers key.
func GetCachedID(bootstrapServers string) (string, bool) {
	if bootstrapServers == "" {
		return "", false
	}
	v, ok := cache.Load(bootstrapServers)
	if !ok {
		return "", false
	}
	return v.(string), true
}

// SetCachedID caches a cluster ID for the given bootstrap servers key.
func SetCachedID(bootstrapServers, clusterID string) {
	if bootstrapServers == "" || clusterID == "" {
		return
	}
	cache.Store(bootstrapServers, clusterID)
}

// ResetCache clears the cluster ID cache. This is intended for use in tests.
func ResetCache() {
	cache = sync.Map{}
}
