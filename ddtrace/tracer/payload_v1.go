// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

// payloadV1 is a new version of a msgp payload that can be sent to the agent.
// Be aware that payloadV1 follows the same rules and constraints as payloadV04. That is:
//
// payloadV1 is not safe for concurrent use
//
// payloadV1 is meant to be used only once and eventually dismissed with the
// single exception of retrying failed flush attempts.
//
// ⚠️  Warning!
//
// The payloadV1 should not be reused for multiple sets of traces.  Resetting the
// payloadV1 for re-use requires the transport to wait for the HTTP package
// Close the request body before attempting to re-use it again!
type payloadV1 struct {
	// array of strings referenced in this tracer payload, its chunks and spans
	strings []string

	// the string ID of the container where the tracer is running
	containerId uint32

	// the string language name of the tracer
	languageName uint32

	// the string language version of the tracer
	languageVersion uint32

	// the string version of the tracer
	tracerVersion uint32

	// the V4 string UUID representation of a tracer session
	runtimeId uint32

	// the optional `env` string tag that set with the tracer
	env uint32

	// the optional string hostname of where the tracer is running
	hostname uint32

	// the optional string `version` tag for the application set in the tracer
	appVersion uint32

	// a collection of key to value pairs common in all `chunks`
	attributes map[uint32]any // TODO: this should be compatible with AnyValue

	// a list of trace `chunks`
	chunks []traceChunk
}
