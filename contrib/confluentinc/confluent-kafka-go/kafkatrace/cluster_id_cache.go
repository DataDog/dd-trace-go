// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"slices"
	"strings"
	"sync"
)

var clusterIDCache sync.Map // normalized bootstrap servers -> cluster ID

// NormalizeBootstrapServers returns a canonical form of a comma-separated
// bootstrap servers string. It trims whitespace, removes empty entries,
// and sorts entries lexicographically.
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

// GetCachedClusterID returns the cached cluster ID for the given normalized
// bootstrap servers string, and whether it was found.
func GetCachedClusterID(normalizedBootstrapServers string) (string, bool) {
	if normalizedBootstrapServers == "" {
		return "", false
	}
	v, ok := clusterIDCache.Load(normalizedBootstrapServers)
	if !ok {
		return "", false
	}
	return v.(string), true
}

// SetCachedClusterID stores a cluster ID for the given normalized bootstrap
// servers. Empty keys or values are silently ignored.
func SetCachedClusterID(normalizedBootstrapServers, clusterID string) {
	if normalizedBootstrapServers == "" || clusterID == "" {
		return
	}
	clusterIDCache.Store(normalizedBootstrapServers, clusterID)
}

// ResetClusterIDCache clears the cluster ID cache. Intended for use in tests.
func ResetClusterIDCache() {
	clusterIDCache = sync.Map{}
}
