// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import "github.com/DataDog/dd-trace-go/v2/llmobs"

// Kind is the LLM Obs span kind.
type Kind = llmobs.SpanKind

const (
	KindLLM       Kind = llmobs.SpanKindLLM
	KindAgent     Kind = llmobs.SpanKindAgent
	KindWorkflow  Kind = llmobs.SpanKindWorkflow
	KindTask      Kind = llmobs.SpanKindTask
	KindTool      Kind = llmobs.SpanKindTool
	KindEmbedding Kind = llmobs.SpanKindEmbedding
	KindRetrieval Kind = llmobs.SpanKindRetrieval
)

// Status is the terminal status of a span.
type Status = llmobs.SpanStatus

const (
	StatusOK    Status = llmobs.SpanStatusOK
	StatusError Status = llmobs.SpanStatusError
)

// SpanEvent is a caller-built LLM Obs span.
type SpanEvent = llmobs.SpanEvent

// SpanMetrics contains optional metrics for a manually constructed span.
type SpanMetrics = llmobs.SpanMetrics

// SpanLink links a manually constructed span to another span.
type SpanLink = llmobs.SpanEventLink
