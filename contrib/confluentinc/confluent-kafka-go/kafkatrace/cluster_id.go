// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import "github.com/DataDog/dd-trace-go/v2/internal/kafkaclusterid"

// NormalizeBootstrapServers returns a canonical form of a comma-separated list
// of broker addresses.
func NormalizeBootstrapServers(bootstrapServers string) string {
	return kafkaclusterid.NormalizeBootstrapServers(bootstrapServers)
}

// GetCachedClusterID returns a cached cluster ID for the given bootstrap servers key.
func GetCachedClusterID(bootstrapServers string) (string, bool) {
	return kafkaclusterid.GetCachedID(bootstrapServers)
}

// SetCachedClusterID caches a cluster ID for the given bootstrap servers key.
func SetCachedClusterID(bootstrapServers, clusterID string) {
	kafkaclusterid.SetCachedID(bootstrapServers, clusterID)
}

// ResetClusterIDCache clears the cluster ID cache. This is intended for use in tests.
func ResetClusterIDCache() {
	kafkaclusterid.ResetCache()
}
