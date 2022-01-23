// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:generate msgp -unexported -marshal=false -o=payload_msgp.go -tests=false

package pipelines

// statsPayload stores client computed stats.
type statsPayload struct {
	// Env specifies the env. of the application, as defined by the user.
	Env string
	// Stats holds all stats buckets computed within this payload.
	Stats []statsBucket
}

// statsBucket specifies a set of stats computed over a duration.
type statsBucket struct {
	// Start specifies the beginning of this bucket.
	Start uint64
	// Duration specifies the duration of this bucket.
	Duration uint64
	// Stats contains a set of statistics computed for the duration of this bucket.
	Stats []groupedStats
}

// groupedStats contains a set of statistics grouped under various aggregation keys.
type groupedStats struct {
	// These fields indicate the properties under which the stats were aggregated.
	Service    string
	Edge       string
	Hash       uint64
	ParentHash uint64
	// These fields specify the stats for the above aggregation.
	PathwayLatency []byte
	EdgeLatency    []byte
}
