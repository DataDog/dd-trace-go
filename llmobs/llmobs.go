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

func SpanFromContext(ctx context.Context) (Span, bool) {
	if span, ok := illmobs.ActiveLLMSpanFromContext(ctx); ok {
		return &llmobsBaseSpan{Span: span}, true
	}
	return nil, false
}

// StartLLMSpan starts an LLMObs span of kind LLM.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartLLMSpan(ctx context.Context, name string, opts ...StartSpanOption) (LLMSpan, context.Context) {
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindLLM, name, opts...)
	if !ok {
		return &noopSpan{}, ctx
	}
	return &llmSpan{s}, ctx
}

// StartWorkflowSpan starts an LLMObs span of kind Workflow.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartWorkflowSpan(ctx context.Context, name string, opts ...StartSpanOption) (WorkflowSpan, context.Context) {
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindWorkflow, name, opts...)
	if !ok {
		return &noopSpan{}, ctx
	}
	return &textIOSpan{s}, ctx
}

// StartAgentSpan starts an LLMObs span of kind Agent.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartAgentSpan(ctx context.Context, name string, opts ...StartSpanOption) (AgentSpan, context.Context) {
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindAgent, name, opts...)
	if !ok {
		return &noopSpan{}, ctx
	}
	return &textIOSpan{s}, ctx
}

// StartToolSpan starts an LLMObs span of kind Tool.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartToolSpan(ctx context.Context, name string, opts ...StartSpanOption) (ToolSpan, context.Context) {
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindTool, name, opts...)
	if !ok {
		return &noopSpan{}, ctx
	}
	return &textIOSpan{s}, ctx
}

// StartTaskSpan starts an LLMObs span of kind Task.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartTaskSpan(ctx context.Context, name string, opts ...StartSpanOption) (TaskSpan, context.Context) {
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindTask, name, opts...)
	if !ok {
		return &noopSpan{}, ctx
	}
	return &textIOSpan{s}, ctx
}

// StartEmbeddingSpan starts an LLMObs span of kind Embedding.
// Pass the returned context to subsequent start span calls to create child spans of this one.
//
// Note: when annotating an embedding span’s input you should use the WithEmbeddingInput option instead of the generic one.
func StartEmbeddingSpan(ctx context.Context, name string, opts ...StartSpanOption) (EmbeddingSpan, context.Context) {
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindEmbedding, name, opts...)
	if !ok {
		return &noopSpan{}, ctx
	}
	return &embeddingSpan{s}, ctx
}

// StartRetrievalSpan starts an LLMObs span of kind Retrieval.
// Pass the returned context to subsequent start span calls to create child spans of this one.
//
// Note: when annotating a retrieval span’s output you should use the WithAnnotatedRetrievedDocumentOutput option.
func StartRetrievalSpan(ctx context.Context, name string, opts ...StartSpanOption) (RetrievalSpan, context.Context) {
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindRetrieval, name, opts...)
	if !ok {
		return &noopSpan{}, ctx
	}
	return &retrievalSpan{s}, ctx
}

type (
	SpanLink          = illmobs.SpanLink
	LLMMessage        = illmobs.LLMMessage
	EmbeddedDocument  = illmobs.EmbeddedDocument
	RetrievedDocument = illmobs.RetrievedDocument
	Prompt            = illmobs.Prompt
	ToolDefinition    = illmobs.ToolDefinition
)

type (
	Span interface {
		baseSpan
		spanConverter
	}
	// LLMSpan represents a span of kind llm
	LLMSpan interface {
		baseSpan
		llmIOAnnotator
	}
	// WorkflowSpan represents a span of kind workflow
	WorkflowSpan interface {
		baseSpan
		textIOAnnotator
	}
	// AgentSpan represents a span of kind agent
	AgentSpan interface {
		baseSpan
		textIOAnnotator
	}
	// ToolSpan represents a span of kind tool
	ToolSpan interface {
		baseSpan
		textIOAnnotator
	}
	// TaskSpan represents a span of kind task
	TaskSpan interface {
		baseSpan
		textIOAnnotator
	}
	// EmbeddingSpan represents a span of kind embedding
	EmbeddingSpan interface {
		baseSpan
		embeddingIOAnnotator
	}
	// RetrievalSpan represents a span of kind retrieval
	RetrievalSpan interface {
		baseSpan
		retrievalIOAnnotator
	}
)

type baseSpan interface {
	sealed()
	SpanID() string
	Kind() string
	TraceID() string
	APMTraceID() string
	AddLink(link SpanLink)
	Finish(opts ...FinishSpanOption)
}

type spanConverter interface {
	AsLLM() (LLMSpan, bool)
	AsWorkflow() (WorkflowSpan, bool)
	AsAgent() (AgentSpan, bool)
	AsTool() (ToolSpan, bool)
	AsTask() (TaskSpan, bool)
	AsEmbedding() (EmbeddingSpan, bool)
	AsRetrieval() (RetrievalSpan, bool)
}

type textIOAnnotator interface {
	AnnotateTextIO(input, output string, opts ...AnnotateOption)
}

type llmIOAnnotator interface {
	AnnotateLLMIO(input, output []LLMMessage, opts ...AnnotateOption)
}

type embeddingIOAnnotator interface {
	AnnotateEmbeddingIO(input []EmbeddedDocument, output string, opts ...AnnotateOption)
}

type retrievalIOAnnotator interface {
	AnnotateRetrievalIO(input string, output []RetrievedDocument, opts ...AnnotateOption)
}

type llmobsBaseSpan struct {
	*illmobs.Span
}

func (s *llmobsBaseSpan) AsLLM() (LLMSpan, bool) {
	if illmobs.SpanKind(s.Kind()) == illmobs.SpanKindLLM {
		return &llmSpan{s}, true
	}
	return nil, false
}

func (s *llmobsBaseSpan) AsWorkflow() (WorkflowSpan, bool) {
	return s.asTextIO(illmobs.SpanKindWorkflow)
}

func (s *llmobsBaseSpan) AsAgent() (AgentSpan, bool) {
	return s.asTextIO(illmobs.SpanKindWorkflow)
}

func (s *llmobsBaseSpan) AsTool() (ToolSpan, bool) {
	return s.asTextIO(illmobs.SpanKindWorkflow)
}

func (s *llmobsBaseSpan) AsTask() (TaskSpan, bool) {
	return s.asTextIO(illmobs.SpanKindWorkflow)
}

func (s *llmobsBaseSpan) AsEmbedding() (EmbeddingSpan, bool) {
	if illmobs.SpanKind(s.Kind()) == illmobs.SpanKindEmbedding {
		return &embeddingSpan{s}, true
	}
	return nil, false
}

func (s *llmobsBaseSpan) AsRetrieval() (RetrievalSpan, bool) {
	if illmobs.SpanKind(s.Kind()) == illmobs.SpanKindRetrieval {
		return &retrievalSpan{s}, true
	}
	return nil, false
}

func (s *llmobsBaseSpan) asTextIO(target illmobs.SpanKind) (*textIOSpan, bool) {
	if illmobs.SpanKind(s.Kind()) == target {
		return &textIOSpan{s}, true
	}
	return nil, false
}

func (*llmobsBaseSpan) sealed() {}

func (s *llmobsBaseSpan) Finish(opts ...FinishSpanOption) {
	cfg := illmobs.FinishSpanConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	s.Span.Finish(cfg)
}

type textIOSpan struct {
	*llmobsBaseSpan
}

func (s *textIOSpan) AnnotateTextIO(input, output string, opts ...AnnotateOption) {
	a := parseAnnotateOptions(opts...)
	a.InputText = input
	a.OutputText = output
	s.Span.Annotate(a)
}

type llmSpan struct {
	*llmobsBaseSpan
}

func (s *llmSpan) AnnotateLLMIO(input, output []LLMMessage, opts ...AnnotateOption) {
	a := parseAnnotateOptions(opts...)
	a.InputMessages = input
	a.OutputMessages = output
	s.Span.Annotate(a)
}

type embeddingSpan struct {
	*llmobsBaseSpan
}

func (s *embeddingSpan) AnnotateEmbeddingIO(input []EmbeddedDocument, output string, opts ...AnnotateOption) {
	a := parseAnnotateOptions(opts...)
	a.InputEmbeddedDocs = input
	a.OutputText = output
	s.Span.Annotate(a)
}

type retrievalSpan struct {
	*llmobsBaseSpan
}

func (s *retrievalSpan) AnnotateRetrievalIO(input string, output []RetrievedDocument, opts ...AnnotateOption) {
	a := parseAnnotateOptions(opts...)
	a.InputText = input
	a.OutputRetrievedDocs = output
	s.Span.Annotate(a)
}

type noopSpan struct{}

func (s *noopSpan) sealed()                                                                  {}
func (s *noopSpan) SpanID() string                                                           { return "" }
func (s *noopSpan) Kind() string                                                             { return "" }
func (s *noopSpan) TraceID() string                                                          { return "" }
func (s *noopSpan) APMTraceID() string                                                       { return "" }
func (s *noopSpan) AddLink(_ SpanLink)                                                       {}
func (s *noopSpan) Finish(_ ...FinishSpanOption)                                             {}
func (s *noopSpan) AnnotateTextIO(_, _ string, _ ...AnnotateOption)                          {}
func (s *noopSpan) AnnotateLLMIO(_, _ []LLMMessage, _ ...AnnotateOption)                     {}
func (s *noopSpan) AnnotateEmbeddingIO(_ []EmbeddedDocument, _ string, _ ...AnnotateOption)  {}
func (s *noopSpan) AnnotateRetrievalIO(_ string, _ []RetrievedDocument, _ ...AnnotateOption) {}

func startSpan(ctx context.Context, kind illmobs.SpanKind, name string, opts ...StartSpanOption) (*llmobsBaseSpan, context.Context, bool) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to start llmobs span: %v", err)
		return nil, ctx, false
	}
	cfg := illmobs.StartSpanConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	s, ctx := ll.StartSpan(ctx, kind, name, cfg)
	return &llmobsBaseSpan{s}, ctx, true
}

func parseAnnotateOptions(opts ...AnnotateOption) illmobs.SpanAnnotations {
	a := illmobs.SpanAnnotations{}
	for _, opt := range opts {
		opt(&a)
	}
	return a
}
