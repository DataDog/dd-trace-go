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

// StartLLMSpan starts an LLMObs span of kind LLM.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartLLMSpan(ctx context.Context, name string, opts ...StartLLMSpanOption) (LLMSpan, context.Context) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to start llmobs span: %v", err)
		return nil, ctx
	}
	cfg := llmConfig{}
	for _, o := range opts {
		o.applyLLM(&cfg)
	}

	s, ctx := ll.StartSpan(ctx, illmobs.SpanKindLLM, name, cfg.startSpanConfig())
	return &llmSpan{baseSpan{s}}, ctx
}

// StartWorkflowSpan starts an LLMObs span of kind Workflow.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartWorkflowSpan(ctx context.Context, name string, opts ...StartWorkflowSpanOption) (WorkflowSpan, context.Context) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to start llmobs span: %v", err)
		return nil, ctx
	}
	cfg := commonConfig{}
	for _, o := range opts {
		o.applyCommon(&cfg)
	}

	s, ctx := ll.StartSpan(ctx, illmobs.SpanKindLLM, name, cfg.startSpanConfig())
	return &workflowSpan{baseSpan{s}}, ctx
}

// StartAgentSpan starts an LLMObs span of kind Agent.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartAgentSpan(ctx context.Context, name string, opts ...StartAgentSpanOption) (AgentSpan, context.Context) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to start llmobs span: %v", err)
		return nil, ctx
	}
	cfg := commonConfig{}
	for _, o := range opts {
		o.applyCommon(&cfg)
	}

	s, ctx := ll.StartSpan(ctx, illmobs.SpanKindAgent, name, cfg.startSpanConfig())
	return &agentSpan{baseSpan{s}}, ctx
}

// StartToolSpan starts an LLMObs span of kind Tool.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartToolSpan(ctx context.Context, name string, opts ...StartToolSpanOption) (ToolSpan, context.Context) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to start llmobs span: %v", err)
		return nil, ctx
	}
	cfg := commonConfig{}
	for _, o := range opts {
		o.applyCommon(&cfg)
	}

	s, ctx := ll.StartSpan(ctx, illmobs.SpanKindTool, name, cfg.startSpanConfig())
	return &toolSpan{baseSpan{s}}, ctx
}

// StartTaskSpan starts an LLMObs span of kind Task.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartTaskSpan(ctx context.Context, name string, opts ...StartTaskSpanOption) (TaskSpan, context.Context) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to start llmobs span: %v", err)
		return nil, ctx
	}
	cfg := commonConfig{}
	for _, o := range opts {
		o.applyCommon(&cfg)
	}

	s, ctx := ll.StartSpan(ctx, illmobs.SpanKindTask, name, cfg.startSpanConfig())
	return &taskSpan{baseSpan{s}}, ctx
}

// StartEmbeddingSpan starts an LLMObs span of kind Embedding.
// Pass the returned context to subsequent start span calls to create child spans of this one.
//
// Note: when annotating an embedding span’s input you should use the WithEmbeddingInput option instead of the generic one.
func StartEmbeddingSpan(ctx context.Context, name string, opts ...StartEmbeddingSpanOption) (EmbeddingSpan, context.Context) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to start llmobs span: %v", err)
		return nil, ctx
	}
	cfg := llmConfig{}
	for _, o := range opts {
		o.applyLLM(&cfg)
	}

	s, ctx := ll.StartSpan(ctx, illmobs.SpanKindEmbedding, name, cfg.startSpanConfig())
	return &embeddingSpan{baseSpan{s}}, ctx
}

// StartRetrievalSpan starts an LLMObs span of kind Retrieval.
// Pass the returned context to subsequent start span calls to create child spans of this one.
//
// Note: when annotating a retrieval span’s output you should use the WithAnnotatedRetrievedDocumentOutput option.
func StartRetrievalSpan(ctx context.Context, name string, opts ...StartRetrievalSpanOption) (RetrievalSpan, context.Context) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to start llmobs span: %v", err)
		return nil, ctx
	}
	cfg := commonConfig{}
	for _, o := range opts {
		o.applyCommon(&cfg)
	}

	s, ctx := ll.StartSpan(ctx, illmobs.SpanKindRetrieval, name, cfg.startSpanConfig())
	return &retrievalSpan{baseSpan{s}}, ctx
}

type (
	SpanLink          = illmobs.SpanLink
	LLMMessage        = illmobs.LLMMessage
	EmbeddedDocument  = illmobs.EmbeddedDocument
	RetrievedDocument = illmobs.RetrievedDocument
	Prompt            = illmobs.Prompt
)

type (
	// LLMSpan represents a span of kind llm
	LLMSpan interface {
		span
		AnnotateIO(input, output []LLMMessage, opts ...AnnotateOption)
		AnnotatePrompt(prompt Prompt)
	}
	// WorkflowSpan represents a span of kind workflow
	WorkflowSpan interface {
		span
		AnnotateIO(input, output string, opts ...AnnotateOption)
	}
	// AgentSpan represents a span of kind agent
	AgentSpan interface {
		span
		AnnotateIO(input, output string, opts ...AnnotateOption)
		AnnotateAgentManifest(manifest string)
	}
	// ToolSpan represents a span of kind tool
	ToolSpan interface {
		span
		AnnotateIO(input, output string, opts ...AnnotateOption)
	}
	// TaskSpan represents a span of kind task
	TaskSpan interface {
		span
		AnnotateIO(input, output string, opts ...AnnotateOption)
	}
	// EmbeddingSpan represents a span of kind embedding
	EmbeddingSpan interface {
		span
		AnnotateIO(input []EmbeddedDocument, output string, opts ...AnnotateOption)
	}
	// RetrievalSpan represents a span of kind retrieval
	RetrievalSpan interface {
		span
		AnnotateIO(input string, output []RetrievedDocument, opts ...AnnotateOption)
	}
)

type span interface {
	sealed()

	SpanID() string
	TraceID() string
	APMTraceID() string
	AddLink(link SpanLink)
	Finish(opts ...FinishSpanOption)
}

type baseSpan struct {
	*illmobs.Span
}

func (*baseSpan) sealed() {}

func (s *baseSpan) Finish(opts ...FinishSpanOption) {
	cfg := illmobs.FinishSpanConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	s.Span.Finish(cfg)
}

type (
	llmSpan struct {
		baseSpan
	}
	workflowSpan struct {
		baseSpan
	}
	agentSpan struct {
		baseSpan
	}
	toolSpan struct {
		baseSpan
	}
	taskSpan struct {
		baseSpan
	}
	embeddingSpan struct {
		baseSpan
	}
	retrievalSpan struct {
		baseSpan
	}
)

func (s *llmSpan) AnnotateIO(input, output []LLMMessage, opts ...AnnotateOption) {
	a := parseAnnotateOptions(opts...)
	a.InputMessages = input
	a.OutputMessages = output
	s.Span.Annotate(a)
}

func (s *llmSpan) AnnotatePrompt(prompt Prompt) {
	a := illmobs.SpanAnnotations{Prompt: &prompt}
	s.Span.Annotate(a)
}

func (s *retrievalSpan) AnnotateIO(input string, output []RetrievedDocument, opts ...AnnotateOption) {
	a := parseAnnotateOptions(opts...)
	a.InputText = input
	a.OutputRetrievedDocs = output
	s.Span.Annotate(a)
}

func (s *embeddingSpan) AnnotateIO(input []EmbeddedDocument, output string, opts ...AnnotateOption) {
	a := parseAnnotateOptions(opts...)
	a.InputEmbeddedDocs = input
	a.OutputText = output
	s.Span.Annotate(a)
}

func (s *taskSpan) AnnotateIO(input, output string, opts ...AnnotateOption) {
	annotateIOText(s.Span, input, output, opts...)
}

func (s *toolSpan) AnnotateIO(input, output string, opts ...AnnotateOption) {
	annotateIOText(s.Span, input, output, opts...)
}

func (s *agentSpan) AnnotateIO(input, output string, opts ...AnnotateOption) {
	annotateIOText(s.Span, input, output, opts...)
}

func (s *agentSpan) AnnotateAgentManifest(manifest string) {
	a := illmobs.SpanAnnotations{AgentManifest: manifest}
	s.Span.Annotate(a)
}

func (s *workflowSpan) AnnotateIO(input, output string, opts ...AnnotateOption) {
	annotateIOText(s.Span, input, output, opts...)
}

func parseAnnotateOptions(opts ...AnnotateOption) illmobs.SpanAnnotations {
	a := illmobs.SpanAnnotations{}
	for _, opt := range opts {
		opt(&a)
	}
	return a
}

func annotateIOText(s *illmobs.Span, input, output string, opts ...AnnotateOption) {
	a := parseAnnotateOptions(opts...)
	a.InputText = input
	a.OutputText = output
	s.Annotate(a)
}
