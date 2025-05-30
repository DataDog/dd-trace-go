// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

type uploadEvent struct {
	Start            string            `json:"start"`
	End              string            `json:"end"`
	Attachments      []string          `json:"attachments"`
	Tags             string            `json:"tags_profiler"`
	Family           string            `json:"family"`
	Version          string            `json:"version"`
	EndpointCounts   map[string]uint64 `json:"endpoint_counts,omitempty"`
	CustomAttributes []string          `json:"custom_attributes,omitempty"`
	Info             struct {
		Profiler profilerInfo `json:"profiler"`
	} `json:"info"`
}

// profilerInfo holds profiler-specific information which should be attached to
// the event for backend consumption
type profilerInfo struct {
	SSI struct {
		Mechanism string `json:"mechanism,omitempty"`
	} `json:"ssi"`
	// Activation distinguishes how the profiler was enabled, either "auto"
	// (env var set via admission controller) or "manual"
	Activation string `json:"activation"`
}
