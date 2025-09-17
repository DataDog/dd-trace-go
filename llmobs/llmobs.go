// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package llmobs contains the Go SDK to use DataDog's LLM Observability product.
// You can read more at https://docs.datadoghq.com/llm_observability
//
// EXPERIMENTAL: This package is experimental and may change or be removed at any time
// without notice. It is not subject to the Go module's compatibility promise.
package llmobs

import (
	"context"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// Reference: https://docs.datadoghq.com/llm_observability/instrumentation/sdk/

// type SpanKind string

//func Start(opts ...Option) error {
//	return illmobs.Start(opts...)
//}
//
//func Stop() {
//	internal.Stop()
//}

type Span = illmobs.Span

func StartWorkflowSpan(ctx context.Context, name string, opts ...StartSpanOption) (*Span, context.Context) {
	llm, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to start llmobs span: %v", err)
		return nil, ctx
	}

	return llm.StartSpan(ctx, illmobs.SpanKindWorkflow, name, opts...)
}

func StartLLMSpan(ctx context.Context, name string, opts ...StartSpanOption) (*Span, context.Context) {
	llm, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to start llmobs span: %v", err)
		return nil, ctx
	}

	return llm.StartSpan(ctx, illmobs.SpanKindWorkflow, name, opts...)
}
