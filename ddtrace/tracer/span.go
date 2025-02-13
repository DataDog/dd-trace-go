// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

const (
	keySamplingPriority = "_sampling_priority_v1"
	keyOrigin           = "_dd.origin"
	// keyHostname can be used to override the agent's hostname detection when using `WithHostname`. Not to be confused with keyTracerHostname
	// which is set via auto-detection.
	keyHostname = "_dd.hostname"
	keyMeasured = "_dd.measured"
	// keyTopLevel is the key of top level metric indicating if a span is top level.
	// A top level span is a local root (parent span of the local trace) or the first span of each service.
	keyTopLevel = "_dd.top_level"
	// keyTraceID128 is the lowercase, hex encoded upper 64 bits of a 128-bit trace id, if present.
	keyTraceID128 = "_dd.p.tid"
)
