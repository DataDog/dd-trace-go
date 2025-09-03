/*
 * Unless explicitly stated otherwise all files in this repository are licensed
 * under the Apache License Version 2.0.
 *
 * This product includes software developed at Datadog (https://www.datadoghq.com/).
 * Copyright 2021 Datadog, Inc.
 */

package trace

const (
	traceIDHeader          = "x-datadog-trace-id"
	parentIDHeader         = "x-datadog-parent-id"
	samplingPriorityHeader = "x-datadog-sampling-priority"
)

const (
	userReject = "-1"
	// autoReject = "0"
	// autoKeep = "1"
	userKeep = "2"
)

const (
	xraySubsegmentName      = "datadog-metadata"
	xraySubsegmentKey       = "trace"
	xraySubsegmentNamespace = "datadog"
)
