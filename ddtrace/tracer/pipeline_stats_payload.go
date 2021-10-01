// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:generate msgp -unexported -marshal=false -o=pipeline_stats_payload_msgp.go -tests=false

package tracer

// pipelineStatsPayload specifies information about client computed pipeline stats and is encoded
// to be sent to the agent.
type pipelineStatsPayload struct {
	// Hostname specifies the hostname of the application.
	Hostname string

	// Env specifies the env. of the application, as defined by the user.
	Env string

	// Version specifies the application version.
	Version string

	// Stats holds all stats buckets computed within this payload.
	Stats []pipelineStatsBucket
}

// pipelineStatsBucket specifies a set of pipeline stats computed over a duration.
type pipelineStatsBucket struct {
	// Start specifies the beginning of this bucket.
	Start uint64

	// Duration specifies the duration of this bucket.
	Duration uint64

	// Stats contains a set of pipeline statistics computed for the duration of this bucket.
	Stats []groupedPipelineStats
}

// groupedPipelineStats contains a set of pipeline statistics grouped under various aggregation keys.
type groupedPipelineStats struct {
	// These fields indicate the properties under which the stats were aggregated.
	Service        string `json:"service,omitempty"`
	ReceivingPipelineName       string `json:"receiving_pipeline_name,omitempty"`
	PipelineHash         uint64 `json:"pipeline_hash,omitempty"`

	// These fields specify the stats for the above aggregation.
	Summary    []byte `json:"summary,omitempty"`
}
