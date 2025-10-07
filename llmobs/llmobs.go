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

// SpanFromContext retrieves the active LLMObs span from the given context.
// Returns the span and true if found, nil and false otherwise.
// The returned span can be converted to specific span types using AsLLM(), AsWorkflow(), etc.
func SpanFromContext(ctx context.Context) (Span, bool) {
	if span, ok := illmobs.ActiveLLMSpanFromContext(ctx); ok {
		return &baseSpan{span}, true
	}
	return nil, false
}

// StartLLMSpan starts an LLMObs span of kind LLM.
// Pass the returned context to subsequent start span calls to create child spans of this one.
//
// Note: LLM spans are annotated with input/output as LLMMessage.
func StartLLMSpan(ctx context.Context, name string, opts ...StartSpanOption) (*LLMSpan, context.Context) {
	var l LLMSpan
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindLLM, name, opts...)
	if !ok {
		return &l, ctx
	}
	l.baseSpan = s
	return &l, ctx
}

// StartWorkflowSpan starts an LLMObs span of kind Workflow.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartWorkflowSpan(ctx context.Context, name string, opts ...StartSpanOption) (*WorkflowSpan, context.Context) {
	var (
		w  WorkflowSpan
		ok bool
	)
	w.baseSpan, ctx, ok = startSpan(ctx, illmobs.SpanKindWorkflow, name, opts...)
	if !ok {
		return &w, ctx
	}
	return &w, ctx
}

// StartAgentSpan starts an LLMObs span of kind Agent.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartAgentSpan(ctx context.Context, name string, opts ...StartSpanOption) (*AgentSpan, context.Context) {
	var a AgentSpan
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindAgent, name, opts...)
	if !ok {
		return &a, ctx
	}
	a.baseSpan = s
	return &a, ctx
}

// StartToolSpan starts an LLMObs span of kind Tool.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartToolSpan(ctx context.Context, name string, opts ...StartSpanOption) (*ToolSpan, context.Context) {
	var t ToolSpan
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindTool, name, opts...)
	if !ok {
		return &t, ctx
	}
	t.baseSpan = s
	return &t, ctx
}

// StartTaskSpan starts an LLMObs span of kind Task.
// Pass the returned context to subsequent start span calls to create child spans of this one.
func StartTaskSpan(ctx context.Context, name string, opts ...StartSpanOption) (*TaskSpan, context.Context) {
	var t TaskSpan
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindTask, name, opts...)
	if !ok {
		return &t, ctx
	}
	t.baseSpan = s
	return &t, ctx
}

// StartEmbeddingSpan starts an LLMObs span of kind Embedding.
// Pass the returned context to subsequent start span calls to create child spans of this one.
//
// Note: embedding spans are annotated with input EmbeddedDocument and output text.
func StartEmbeddingSpan(ctx context.Context, name string, opts ...StartSpanOption) (*EmbeddingSpan, context.Context) {
	var e EmbeddingSpan
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindEmbedding, name, opts...)
	if !ok {
		return &e, ctx
	}
	e.baseSpan = s
	return &e, ctx
}

// StartRetrievalSpan starts an LLMObs span of kind Retrieval.
// Pass the returned context to subsequent start span calls to create child spans of this one.
//
// Note: retrieval spans are annotated with input text and output RetrievedDocument.
func StartRetrievalSpan(ctx context.Context, name string, opts ...StartSpanOption) (*RetrievalSpan, context.Context) {
	var r RetrievalSpan
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindRetrieval, name, opts...)
	if !ok {
		return &r, ctx
	}
	r.baseSpan = s
	return &r, ctx
}

type (
	// SpanLink represents a link between spans, typically used for connecting related operations
	// across different traces or services.
	SpanLink = illmobs.SpanLink

	// LLMMessage represents a message in a conversation with a Large Language Model.
	// It is used to annotate IO of LLM spans.
	// Contains role (user, assistant, system) and content.
	LLMMessage = illmobs.LLMMessage

	// EmbeddedDocument represents a document that can be converted to embeddings.
	// It is used to annotate input of embedding spans.
	EmbeddedDocument = illmobs.EmbeddedDocument

	// RetrievedDocument represents a document retrieved from a knowledge base or search system.
	// Contains the document content, metadata, and relevance score.
	// It is used to annotate output of retrieval spans.
	RetrievedDocument = illmobs.RetrievedDocument

	// Prompt represents a structured prompt template used with LLMs.
	Prompt = illmobs.Prompt

	// ToolDefinition represents the definition of a tool/function that an LLM can call.
	ToolDefinition = illmobs.ToolDefinition
)

// Span represents a generic LLMObs span that can be converted to specific span types.
// Use AsLLM(), AsWorkflow(), etc. to convert to typed spans with specific annotation methods.
type Span interface {
	sealed() // Prevents external implementations

	spanConverter

	// SpanID returns the unique identifier for this span.
	SpanID() string

	// Kind returns the span kind (llm, workflow, agent, tool, task, embedding, retrieval).
	Kind() string

	// TraceID returns the LLMObs trace identifier.
	TraceID() string

	// APMTraceID returns the underlying APM trace identifier.
	APMTraceID() string

	// AddLink adds a link to another span, useful for connecting related operations.
	AddLink(link SpanLink)

	// Finish completes the span and sends it for processing.
	Finish(opts ...FinishSpanOption)
}

// spanConverter provides type conversion methods for generic spans.
// Allows safe conversion from generic Span to specific span types.
type spanConverter interface {
	// AsLLM attempts to convert to an LLMSpan. Returns the span and true if successful.
	AsLLM() (*LLMSpan, bool)

	// AsWorkflow attempts to convert to a WorkflowSpan. Returns the span and true if successful.
	AsWorkflow() (*WorkflowSpan, bool)

	// AsAgent attempts to convert to an AgentSpan. Returns the span and true if successful.
	AsAgent() (*AgentSpan, bool)

	// AsTool attempts to convert to a ToolSpan. Returns the span and true if successful.
	AsTool() (*ToolSpan, bool)

	// AsTask attempts to convert to a TaskSpan. Returns the span and true if successful.
	AsTask() (*TaskSpan, bool)

	// AsEmbedding attempts to convert to an EmbeddingSpan. Returns the span and true if successful.
	AsEmbedding() (*EmbeddingSpan, bool)

	// AsRetrieval attempts to convert to a RetrievalSpan. Returns the span and true if successful.
	AsRetrieval() (*RetrievalSpan, bool)
}

// WorkflowSpan represents a span for high-level workflow operations.
type WorkflowSpan struct {
	textIOSpan
}

// AgentSpan represents a span for AI agent operations.
type AgentSpan struct {
	textIOSpan
}

// ToolSpan represents a span for tool/function call operations.
type ToolSpan struct {
	textIOSpan
}

// TaskSpan represents a span for discrete task operations.
type TaskSpan struct {
	textIOSpan
}

// EmbeddingSpan represents a span for text embedding operations.
type EmbeddingSpan struct {
	*baseSpan
}

func (s *EmbeddingSpan) AnnotateEmbeddingIO(input []EmbeddedDocument, output string, opts ...AnnotateOption) {
	if s.baseSpan == nil {
		return
	}
	a := parseAnnotateOptions(opts...)
	a.InputEmbeddedDocs = input
	a.OutputText = output
	s.Span.Annotate(a)
}

// RetrievalSpan represents a span for information retrieval operations.
type RetrievalSpan struct {
	*baseSpan
}

func (s *RetrievalSpan) AnnotateRetrievalIO(input string, output []RetrievedDocument, opts ...AnnotateOption) {
	if s.baseSpan == nil {
		return
	}
	a := parseAnnotateOptions(opts...)
	a.InputText = input
	a.OutputRetrievedDocs = output
	s.Span.Annotate(a)
}

type baseSpan struct {
	*illmobs.Span
}

func (s *baseSpan) SpanID() string {
	if s == nil {
		return ""
	}
	return s.Span.SpanID()
}

func (s *baseSpan) Kind() string {
	if s == nil {
		return ""
	}
	return s.Span.Kind()
}

func (s *baseSpan) TraceID() string {
	if s == nil {
		return ""
	}
	return s.Span.TraceID()
}

func (s *baseSpan) APMTraceID() string {
	if s == nil {
		return ""
	}
	return s.Span.APMTraceID()
}

func (s *baseSpan) AsLLM() (*LLMSpan, bool) {
	if illmobs.SpanKind(s.Kind()) != illmobs.SpanKindLLM {
		return nil, false
	}
	return &LLMSpan{s}, true
}

func (s *baseSpan) AsWorkflow() (*WorkflowSpan, bool) {
	if !s.isTextIO(illmobs.SpanKindWorkflow) {
		return nil, false
	}
	var w WorkflowSpan
	w.baseSpan = s
	return &w, true
}

func (s *baseSpan) AsAgent() (*AgentSpan, bool) {
	if !s.isTextIO(illmobs.SpanKindAgent) {
		return nil, false
	}
	var a AgentSpan
	a.baseSpan = s
	return &a, true
}

func (s *baseSpan) AsTool() (*ToolSpan, bool) {
	if !s.isTextIO(illmobs.SpanKindTool) {
		return nil, false
	}
	var t ToolSpan
	t.baseSpan = s
	return &t, true
}

func (s *baseSpan) AsTask() (*TaskSpan, bool) {
	if !s.isTextIO(illmobs.SpanKindTask) {
		return nil, false
	}
	var t TaskSpan
	t.baseSpan = s
	return &t, true
}

func (s *baseSpan) AsEmbedding() (*EmbeddingSpan, bool) {
	if illmobs.SpanKind(s.Kind()) != illmobs.SpanKindEmbedding {
		return nil, false
	}
	var e EmbeddingSpan
	e.baseSpan = s
	return &e, true
}

func (s *baseSpan) AsRetrieval() (*RetrievalSpan, bool) {
	if illmobs.SpanKind(s.Kind()) != illmobs.SpanKindRetrieval {
		return nil, false
	}
	var r RetrievalSpan
	r.baseSpan = s
	return &r, true
}

func (s *baseSpan) isTextIO(target illmobs.SpanKind) bool {
	if illmobs.SpanKind(s.Kind()) != target {
		return false
	}
	switch target {
	case illmobs.SpanKindAgent:
		fallthrough
	case illmobs.SpanKindTask:
		fallthrough
	case illmobs.SpanKindTool:
		fallthrough
	case illmobs.SpanKindWorkflow:
		return true
	}
	return false
}

func (*baseSpan) sealed() {}

func (s *baseSpan) Finish(opts ...FinishSpanOption) {
	if s == nil {
		return
	}
	cfg := illmobs.FinishSpanConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	s.Span.Finish(cfg)
}

type textIOSpan struct {
	*baseSpan
}

func (s *textIOSpan) AnnotateTextIO(input, output string, opts ...AnnotateOption) {
	if s.baseSpan == nil {
		return
	}
	a := parseAnnotateOptions(opts...)
	a.InputText = input
	a.OutputText = output
	s.Span.Annotate(a)
}

type LLMSpan struct {
	*baseSpan
}

func (s *LLMSpan) AnnotateLLMIO(input, output []LLMMessage, opts ...AnnotateOption) {
	if s.baseSpan == nil {
		return
	}
	a := parseAnnotateOptions(opts...)
	a.InputMessages = input
	a.OutputMessages = output
	s.Span.Annotate(a)
}

func startSpan(ctx context.Context, kind illmobs.SpanKind, name string, opts ...StartSpanOption) (*baseSpan, context.Context, bool) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		log.Warn("llmobs: failed to start llmobs span: %v", err.Error())
		return nil, ctx, false
	}
	cfg := illmobs.StartSpanConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	s, ctx := ll.StartSpan(ctx, kind, name, cfg)
	return &baseSpan{s}, ctx, true
}

func parseAnnotateOptions(opts ...AnnotateOption) illmobs.SpanAnnotations {
	a := illmobs.SpanAnnotations{}
	for _, opt := range opts {
		opt(&a)
	}
	return a
}
