// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"errors"
	"time"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/errortrace"
	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

const (
	// MetricKeyInputTokens is the standard key for input token count metrics.
	MetricKeyInputTokens = "input_tokens"

	// MetricKeyOutputTokens is the standard key for output token count metrics.
	MetricKeyOutputTokens = "output_tokens"

	// MetricKeyTotalTokens is the standard key for total token count metrics.
	MetricKeyTotalTokens = "total_tokens"
)

// ------------- Start options -------------

// StartSpanOption configures span creation. Use with Start*Span functions.
type StartSpanOption func(cfg *illmobs.StartSpanConfig)

// WithSessionID sets the session identifier for the span.
func WithSessionID(sessionID string) StartSpanOption {
	return func(c *illmobs.StartSpanConfig) {
		c.SessionID = sessionID
	}
}

// WithMLApp sets the ML application name for the span.
// This overrides the global ML app configuration for this specific span.
func WithMLApp(mlApp string) StartSpanOption {
	return func(c *illmobs.StartSpanConfig) {
		c.MLApp = mlApp
	}
}

// WithStartTime sets a custom start time for the span.
// If not provided, the current time is used.
func WithStartTime(t time.Time) StartSpanOption {
	return func(c *illmobs.StartSpanConfig) {
		c.StartTime = t
	}
}

// WithModelProvider sets the model provider for the span (e.g., "openai", "anthropic").
// Used primarily with LLM spans to track which provider is being used.
func WithModelProvider(modelProvider string) StartSpanOption {
	return func(c *illmobs.StartSpanConfig) {
		c.ModelProvider = modelProvider
	}
}

// WithModelName sets the specific model name for the span (e.g., "gpt-4", "claude-3").
// Used primarily with LLM spans to track which model is being used.
func WithModelName(modelName string) StartSpanOption {
	return func(c *illmobs.StartSpanConfig) {
		c.ModelName = modelName
	}
}

func WithIntegration(integration string) StartSpanOption {
	return func(c *illmobs.StartSpanConfig) {
		c.Integration = integration
	}
}

// ------------- Finish options -------------

// FinishSpanOption configures span finishing. Use with span.Finish().
type FinishSpanOption func(cfg *illmobs.FinishSpanConfig)

// WithError marks the finished span with the given error.
// The error will be captured with stack trace information and marked as a span error.
func WithError(err error) FinishSpanOption {
	return func(cfg *illmobs.FinishSpanConfig) {
		var tErr *errortrace.TracerError
		if !errors.As(err, &tErr) {
			tErr = errortrace.WrapN(err, 0, 2)
		}
		cfg.Error = tErr
	}
}

// WithFinishTime sets a custom finish time for the span.
// If not provided, the current time is used when Finish() is called.
func WithFinishTime(t time.Time) FinishSpanOption {
	return func(cfg *illmobs.FinishSpanConfig) {
		cfg.FinishTime = t
	}
}

// ------------- Annotate options -------------

// AnnotateOption configures span annotations. Use with span annotation methods.
type AnnotateOption func(a *illmobs.SpanAnnotations)

// WithAnnotatedTags adds tags to the span annotation.
func WithAnnotatedTags(tags map[string]string) AnnotateOption {
	return func(a *illmobs.SpanAnnotations) {
		if a.Tags == nil {
			a.Tags = make(map[string]string)
		}
		for k, v := range tags {
			a.Tags[k] = v
		}
	}
}

// WithAnnotatedSessionID sets the session ID tag for the span annotation.
// This is a convenience function for setting the session ID tag specifically.
func WithAnnotatedSessionID(sessionID string) AnnotateOption {
	return func(a *illmobs.SpanAnnotations) {
		if a.Tags == nil {
			a.Tags = make(map[string]string)
		}
		a.Tags[illmobs.TagKeySessionID] = sessionID
	}
}

// WithAnnotatedMetadata adds metadata to the span annotation.
// Metadata can contain arbitrary structured data related to the operation.
func WithAnnotatedMetadata(meta map[string]any) AnnotateOption {
	return func(a *illmobs.SpanAnnotations) {
		if a.Metadata == nil {
			a.Metadata = make(map[string]any)
		}
		for k, v := range meta {
			a.Metadata[k] = v
		}
	}
}

// WithAnnotatedMetrics adds metrics to the span annotation.
// Metrics are numeric values that can be aggregated and analyzed.
// Common metrics include token counts, latency, costs, etc.
// Multiple calls to this function will merge the metrics.
func WithAnnotatedMetrics(metrics map[string]float64) AnnotateOption {
	return func(a *illmobs.SpanAnnotations) {
		if a.Metrics == nil {
			a.Metrics = make(map[string]float64)
		}
		for k, v := range metrics {
			a.Metrics[k] = v
		}
	}
}
