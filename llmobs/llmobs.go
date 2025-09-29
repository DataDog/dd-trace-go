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
		return &baseSpan{Span: span}, true
	}
	return nil, false
}

// StartLLMSpan starts an LLMObs span of kind LLM.
// Pass the returned context to subsequent start span calls to create child spans of this one.
//
// Note: LLM spans are annotated with input/output as LLMMessage.
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
// Note: embedding spans are annotated with input EmbeddedDocument and output text.
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
// Note: retrieval spans are annotated with input text and output RetrievedDocument.
func StartRetrievalSpan(ctx context.Context, name string, opts ...StartSpanOption) (RetrievalSpan, context.Context) {
	s, ctx, ok := startSpan(ctx, illmobs.SpanKindRetrieval, name, opts...)
	if !ok {
		return &noopSpan{}, ctx
	}
	return &retrievalSpan{s}, ctx
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

type (
	// Span represents a generic LLMObs span that can be converted to specific span types.
	// Use AsLLM(), AsWorkflow(), etc. to convert to typed spans with specific annotation methods.
	Span interface {
		BaseSpan
		spanConverter
	}

	// LLMSpan represents a span for Large Language Model operations.
	LLMSpan interface {
		BaseSpan
		llmIOAnnotator
	}

	// WorkflowSpan represents a span for high-level workflow operations.
	WorkflowSpan interface {
		BaseSpan
		textIOAnnotator
	}

	// AgentSpan represents a span for AI agent operations.
	AgentSpan interface {
		BaseSpan
		textIOAnnotator
	}

	// ToolSpan represents a span for tool/function call operations.
	ToolSpan interface {
		BaseSpan
		textIOAnnotator
	}

	// TaskSpan represents a span for discrete task operations.
	TaskSpan interface {
		BaseSpan
		textIOAnnotator
	}

	// EmbeddingSpan represents a span for text embedding operations.
	EmbeddingSpan interface {
		BaseSpan
		embeddingIOAnnotator
	}

	// RetrievalSpan represents a span for information retrieval operations.
	RetrievalSpan interface {
		BaseSpan
		retrievalIOAnnotator
	}
)

// BaseSpan defines the common interface for all LLMObs spans.
type BaseSpan interface {
	sealed() // Prevents external implementations

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
	AsLLM() (LLMSpan, bool)

	// AsWorkflow attempts to convert to a WorkflowSpan. Returns the span and true if successful.
	AsWorkflow() (WorkflowSpan, bool)

	// AsAgent attempts to convert to an AgentSpan. Returns the span and true if successful.
	AsAgent() (AgentSpan, bool)

	// AsTool attempts to convert to a ToolSpan. Returns the span and true if successful.
	AsTool() (ToolSpan, bool)

	// AsTask attempts to convert to a TaskSpan. Returns the span and true if successful.
	AsTask() (TaskSpan, bool)

	// AsEmbedding attempts to convert to an EmbeddingSpan. Returns the span and true if successful.
	AsEmbedding() (EmbeddingSpan, bool)

	// AsRetrieval attempts to convert to a RetrievalSpan. Returns the span and true if successful.
	AsRetrieval() (RetrievalSpan, bool)
}

// textIOAnnotator provides annotation methods for spans that work with text input/output.
// Used by Workflow, Agent, Tool, and Task spans.
type textIOAnnotator interface {
	// AnnotateTextIO annotates the span with text input and output.
	// Use annotation options to add tags, metrics, metadata, etc.
	AnnotateTextIO(input, output string, opts ...AnnotateOption)
}

// llmIOAnnotator provides annotation methods for LLM spans.
// Handles conversation messages with roles (user, assistant, system).
type llmIOAnnotator interface {
	// AnnotateLLMIO annotates the span with LLM conversation messages.
	// Input and output should be slices of LLMMessage with appropriate roles.
	AnnotateLLMIO(input, output []LLMMessage, opts ...AnnotateOption)
}

// embeddingIOAnnotator provides annotation methods for embedding spans.
// Handles document input and embedding output.
type embeddingIOAnnotator interface {
	// AnnotateEmbeddingIO annotates the span with documents to embed and the embedding output.
	// Input should be documents to convert to embeddings, output is typically a model identifier.
	AnnotateEmbeddingIO(input []EmbeddedDocument, output string, opts ...AnnotateOption)
}

// retrievalIOAnnotator provides annotation methods for retrieval spans.
// Handles search queries and retrieved documents.
type retrievalIOAnnotator interface {
	// AnnotateRetrievalIO annotates the span with a search query and retrieved documents.
	// Input is the search query, output is the list of documents found.
	AnnotateRetrievalIO(input string, output []RetrievedDocument, opts ...AnnotateOption)
}

type baseSpan struct {
	*illmobs.Span
}

func (s *baseSpan) AsLLM() (LLMSpan, bool) {
	if illmobs.SpanKind(s.Kind()) == illmobs.SpanKindLLM {
		return &llmSpan{s}, true
	}
	return nil, false
}

func (s *baseSpan) AsWorkflow() (WorkflowSpan, bool) {
	return s.asTextIO(illmobs.SpanKindWorkflow)
}

func (s *baseSpan) AsAgent() (AgentSpan, bool) {
	return s.asTextIO(illmobs.SpanKindWorkflow)
}

func (s *baseSpan) AsTool() (ToolSpan, bool) {
	return s.asTextIO(illmobs.SpanKindWorkflow)
}

func (s *baseSpan) AsTask() (TaskSpan, bool) {
	return s.asTextIO(illmobs.SpanKindWorkflow)
}

func (s *baseSpan) AsEmbedding() (EmbeddingSpan, bool) {
	if illmobs.SpanKind(s.Kind()) == illmobs.SpanKindEmbedding {
		return &embeddingSpan{s}, true
	}
	return nil, false
}

func (s *baseSpan) AsRetrieval() (RetrievalSpan, bool) {
	if illmobs.SpanKind(s.Kind()) == illmobs.SpanKindRetrieval {
		return &retrievalSpan{s}, true
	}
	return nil, false
}

func (s *baseSpan) asTextIO(target illmobs.SpanKind) (*textIOSpan, bool) {
	if illmobs.SpanKind(s.Kind()) == target {
		return &textIOSpan{s}, true
	}
	return nil, false
}

func (*baseSpan) sealed() {}

func (s *baseSpan) Finish(opts ...FinishSpanOption) {
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
	a := parseAnnotateOptions(opts...)
	a.InputText = input
	a.OutputText = output
	s.Span.Annotate(a)
}

type llmSpan struct {
	*baseSpan
}

func (s *llmSpan) AnnotateLLMIO(input, output []LLMMessage, opts ...AnnotateOption) {
	a := parseAnnotateOptions(opts...)
	a.InputMessages = input
	a.OutputMessages = output
	s.Span.Annotate(a)
}

type embeddingSpan struct {
	*baseSpan
}

func (s *embeddingSpan) AnnotateEmbeddingIO(input []EmbeddedDocument, output string, opts ...AnnotateOption) {
	a := parseAnnotateOptions(opts...)
	a.InputEmbeddedDocs = input
	a.OutputText = output
	s.Span.Annotate(a)
}

type retrievalSpan struct {
	*baseSpan
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

func startSpan(ctx context.Context, kind illmobs.SpanKind, name string, opts ...StartSpanOption) (*baseSpan, context.Context, bool) {
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
	return &baseSpan{s}, ctx, true
}

func parseAnnotateOptions(opts ...AnnotateOption) illmobs.SpanAnnotations {
	a := illmobs.SpanAnnotations{}
	for _, opt := range opts {
		opt(&a)
	}
	return a
}
