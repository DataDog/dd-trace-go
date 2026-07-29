// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package export

import (
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/llmobs"
)

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
type Status = string

const (
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

// SpanEvent is a completed LLM Obs span.
type SpanEvent = transport.LLMObsSpanEvent

// SpanLink links an LLM Obs span.
type SpanLink = transport.SpanLink

// DDAttributes contains Datadog correlation attributes for a span.
type DDAttributes = transport.DDAttributes
