package llmobs

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

// SpanOptions contains options for creating LLM spans
type SpanOptions struct {
	Name          string
	ModelName     string
	ModelProvider string
	SessionID     string
	MLApp         string
}

// startSpan is an internal function used by the specific span creation functions
func (l *LLMObs) startSpan(ctx context.Context, operationKind string, opts SpanOptions) (context.Context, ddtrace.Span) {
	l.RLock()
	defer l.RUnlock()

	if !l.enabled {
		log.Debug("LLMObs.startSpan called when LLMObs is not enabled")
		// Return a no-op span to avoid nil references
		return ctx, nil // TODO: Return a no-op span
	}

	// Apply defaults
	name := operationKind
	if opts.Name != "" {
		name = opts.Name
	}

	modelName := "custom"
	if opts.ModelName != "" {
		modelName = opts.ModelName
	}

	modelProvider := "custom"
	if opts.ModelProvider != "" {
		modelProvider = opts.ModelProvider
	}

	mlApp := l.mlApp
	if opts.MLApp != "" {
		mlApp = opts.MLApp
	}

	// Start a new span
	span, ctx := tracer.StartSpanFromContext(ctx, name,
		tracer.SpanType(SpanTypeLLM),
		tracer.ResourceName(operationKind),
	)

	// Set common tags
	span.SetTag(keySpanKind, operationKind)

	// Set model-specific tags when applicable
	if operationKind == SpanKindLLM || operationKind == SpanKindEmbedding {
		span.SetTag(keyModelName, modelName)
		span.SetTag(keyModelProvider, modelProvider)
	}

	// Set session ID
	if opts.SessionID != "" {
		span.SetTag(keySessionID, opts.SessionID)
	}

	// Set ML application
	span.SetTag(keyMLApp, mlApp)

	// Apply any pending annotations
	l.applyAnnotations(span)

	return ctx, span
}

// LLM creates a span for an LLM operation
func LLM(ctx context.Context, opts SpanOptions) (context.Context, ddtrace.Span) {
	return GetLLMObs().startSpan(ctx, SpanKindLLM, opts)
}

// Tool creates a span for a tool operation
func Tool(ctx context.Context, opts SpanOptions) (context.Context, ddtrace.Span) {
	return GetLLMObs().startSpan(ctx, SpanKindTool, opts)
}

// Task creates a span for a task operation
func Task(ctx context.Context, opts SpanOptions) (context.Context, ddtrace.Span) {
	return GetLLMObs().startSpan(ctx, SpanKindTask, opts)
}

// Agent creates a span for an agent operation
func Agent(ctx context.Context, opts SpanOptions) (context.Context, ddtrace.Span) {
	return GetLLMObs().startSpan(ctx, SpanKindAgent, opts)
}

// Workflow creates a span for a workflow operation
func Workflow(ctx context.Context, opts SpanOptions) (context.Context, ddtrace.Span) {
	return GetLLMObs().startSpan(ctx, SpanKindWorkflow, opts)
}

// Embedding creates a span for an embedding operation
func Embedding(ctx context.Context, opts SpanOptions) (context.Context, ddtrace.Span) {
	return GetLLMObs().startSpan(ctx, SpanKindEmbedding, opts)
}

// Retrieval creates a span for a retrieval operation
func Retrieval(ctx context.Context, opts SpanOptions) (context.Context, ddtrace.Span) {
	return GetLLMObs().startSpan(ctx, SpanKindRetrieval, opts)
}
