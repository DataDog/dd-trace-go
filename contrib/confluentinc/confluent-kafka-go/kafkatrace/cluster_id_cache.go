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

var clusterIDCache sync.Map

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

func SetCachedClusterID(normalizedBootstrapServers, clusterID string) {
	if normalizedBootstrapServers == "" || clusterID == "" {
		return
	}
	clusterIDCache.Store(normalizedBootstrapServers, clusterID)
}

func ResetClusterIDCache() {
	clusterIDCache = sync.Map{}
}
