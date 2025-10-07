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
// Returns an AnySpan and true if found, nil and false otherwise.
// The returned AnySpan can be converted to specific span types using the As* methods
// (AsLLM, AsWorkflow, AsAgent, AsTool, AsTask, AsEmbedding, AsRetrieval).
func SpanFromContext(ctx context.Context) (*AnySpan, bool) {
	if span, ok := illmobs.ActiveLLMSpanFromContext(ctx); ok {
		return &AnySpan{&baseSpan{span}}, true
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
type Span interface {
	sealed() // Prevents external implementations

	// Name returns the span name.
	Name() string

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

	// Annotate allows to make generic span annotations. If you want to annotate Input/Output, you can use the specific
	// functions from each span kind.
	Annotate(opts ...AnnotateOption)
}

// WorkflowSpan represents a span for high-level workflow operations.
// Use AnnotateTextIO to annotate with text input/output.
type WorkflowSpan struct {
	textIOSpan
}

// AgentSpan represents a span for AI agent operations.
// Use AnnotateTextIO to annotate with text input/output.
type AgentSpan struct {
	textIOSpan
}

// ToolSpan represents a span for tool/function call operations.
// Use AnnotateTextIO to annotate with text input/output.
type ToolSpan struct {
	textIOSpan
}

// TaskSpan represents a span for discrete task operations.
// Use AnnotateTextIO to annotate with text input/output.
type TaskSpan struct {
	textIOSpan
}

// EmbeddingSpan represents a span for text embedding operations.
// Use AnnotateEmbeddingIO to annotate with input documents and output embeddings.
type EmbeddingSpan struct {
	*baseSpan
}

// AnnotateEmbeddingIO annotates the embedding span with input documents and output embeddings text.
func (s *EmbeddingSpan) AnnotateEmbeddingIO(input []EmbeddedDocument, output string, opts ...AnnotateOption) {
	if s.baseSpan == nil {
		return
	}
	a := parseAnnotateOptions(opts...)
	a.InputEmbeddedDocs = input
	a.OutputText = output
	s.span.Annotate(a)
}

// RetrievalSpan represents a span for information retrieval operations.
// Use AnnotateRetrievalIO to annotate with input query and output retrieved documents.
type RetrievalSpan struct {
	*baseSpan
}

// AnnotateRetrievalIO annotates the retrieval span with input query text and output retrieved documents.
func (s *RetrievalSpan) AnnotateRetrievalIO(input string, output []RetrievedDocument, opts ...AnnotateOption) {
	if s.baseSpan == nil {
		return
	}
	a := parseAnnotateOptions(opts...)
	a.InputText = input
	a.OutputRetrievedDocs = output
	s.span.Annotate(a)
}

// AnySpan represents a generic LLMObs span retrieved from context.
// It can represent any span kind (LLM, Workflow, Agent, Tool, Task, Embedding, or Retrieval).
// Use the As* methods to convert it to a specific span type for type-specific operations.
type AnySpan struct {
	*baseSpan
}

// AsLLM attempts to convert the span to an LLMSpan.
// Returns the LLMSpan and true if the span is of kind LLM, otherwise returns nil and false.
func (s *AnySpan) AsLLM() (*LLMSpan, bool) {
	if !isSpanKind(s, illmobs.SpanKindLLM) {
		return nil, false
	}
	var l LLMSpan
	l.baseSpan = s.baseSpan
	return &l, true
}

// AsWorkflow attempts to convert the span to a WorkflowSpan.
// Returns the WorkflowSpan and true if the span is of kind Workflow, otherwise returns nil and false.
func (s *AnySpan) AsWorkflow() (*WorkflowSpan, bool) {
	if !isSpanKind(s, illmobs.SpanKindWorkflow) {
		return nil, false
	}
	var w WorkflowSpan
	w.baseSpan = s.baseSpan
	return &w, true
}

// AsAgent attempts to convert the span to an AgentSpan.
// Returns the AgentSpan and true if the span is of kind Agent, otherwise returns nil and false.
func (s *AnySpan) AsAgent() (*AgentSpan, bool) {
	if !isSpanKind(s, illmobs.SpanKindAgent) {
		return nil, false
	}
	var a AgentSpan
	a.baseSpan = s.baseSpan
	return &a, true
}

// AsTool attempts to convert the span to a ToolSpan.
// Returns the ToolSpan and true if the span is of kind Tool, otherwise returns nil and false.
func (s *AnySpan) AsTool() (*ToolSpan, bool) {
	if !isSpanKind(s, illmobs.SpanKindTool) {
		return nil, false
	}
	var t ToolSpan
	t.baseSpan = s.baseSpan
	return &t, true
}

// AsTask attempts to convert the span to a TaskSpan.
// Returns the TaskSpan and true if the span is of kind Task, otherwise returns nil and false.
func (s *AnySpan) AsTask() (*TaskSpan, bool) {
	if !isSpanKind(s, illmobs.SpanKindTask) {
		return nil, false
	}
	var t TaskSpan
	t.baseSpan = s.baseSpan
	return &t, true
}

// AsEmbedding attempts to convert the span to an EmbeddingSpan.
// Returns the EmbeddingSpan and true if the span is of kind Embedding, otherwise returns nil and false.
func (s *AnySpan) AsEmbedding() (*EmbeddingSpan, bool) {
	if !isSpanKind(s, illmobs.SpanKindEmbedding) {
		return nil, false
	}
	var e EmbeddingSpan
	e.baseSpan = s.baseSpan
	return &e, true
}

// AsRetrieval attempts to convert the span to a RetrievalSpan.
// Returns the RetrievalSpan and true if the span is of kind Retrieval, otherwise returns nil and false.
func (s *AnySpan) AsRetrieval() (*RetrievalSpan, bool) {
	if !isSpanKind(s, illmobs.SpanKindRetrieval) {
		return nil, false
	}
	var r RetrievalSpan
	r.baseSpan = s.baseSpan
	return &r, true
}

type baseSpan struct {
	span *illmobs.Span
}

func (s *baseSpan) Name() string {
	if s == nil || s.span == nil {
		return ""
	}
	return s.span.Name()
}

func (s *baseSpan) Annotate(opts ...AnnotateOption) {
	if s == nil || s.span == nil {
		return
	}
	a := parseAnnotateOptions(opts...)
	s.span.Annotate(a)
}

func (s *baseSpan) AddLink(link SpanLink) {
	if s == nil || s.span == nil {
		return
	}
	s.span.AddLink(link)
}

func (s *baseSpan) SpanID() string {
	if s == nil || s.span == nil {
		return ""
	}
	return s.span.SpanID()
}

func (s *baseSpan) Kind() string {
	if s == nil || s.span == nil {
		return ""
	}
	return s.span.Kind()
}

func (s *baseSpan) TraceID() string {
	if s == nil || s.span == nil {
		return ""
	}
	return s.span.TraceID()
}

func (s *baseSpan) APMTraceID() string {
	if s == nil || s.span == nil {
		return ""
	}
	return s.span.APMTraceID()
}

func (*baseSpan) sealed() {}

func (s *baseSpan) Finish(opts ...FinishSpanOption) {
	if s == nil || s.span == nil {
		return
	}
	cfg := illmobs.FinishSpanConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	s.span.Finish(cfg)
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
	s.span.Annotate(a)
}

// LLMSpan represents a span for Large Language Model operations.
// Use AnnotateLLMIO to annotate with LLM-specific input/output messages.
type LLMSpan struct {
	*baseSpan
}

// AnnotateLLMIO annotates the LLM span with input and output messages.
// Messages should use the LLMMessage type with role and content.
func (s *LLMSpan) AnnotateLLMIO(input, output []LLMMessage, opts ...AnnotateOption) {
	if s.baseSpan == nil {
		return
	}
	a := parseAnnotateOptions(opts...)
	a.InputMessages = input
	a.OutputMessages = output
	s.span.Annotate(a)
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

func isSpanKind(s Span, target illmobs.SpanKind) bool {
	return illmobs.SpanKind(s.Kind()) == target
}
