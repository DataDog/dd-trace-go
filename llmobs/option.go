// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package llmobs

import (
	"time"

	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
)

const (
	MetricKeyInputTokens  = "input_tokens"
	MetricKeyOutputTokens = "output_tokens"
	MetricKeyTotalTokens  = "total_tokens"
)

// ------------- Start options -------------

type StartSpanOption = func(cfg *illmobs.StartSpanConfig)

func WithSessionID(sessionID string) StartSpanOption {
	return func(c *illmobs.StartSpanConfig) {
		c.SessionID = sessionID
	}
}

func WithMLApp(mlApp string) StartSpanOption {
	return func(c *illmobs.StartSpanConfig) {
		c.MLApp = mlApp
	}
}

func WithStartTime(t time.Time) StartSpanOption {
	return func(c *illmobs.StartSpanConfig) {
		c.StartTime = t
	}
}

func WithModelProvider(modelProvider string) StartSpanOption {
	return func(c *illmobs.StartSpanConfig) {
		c.ModelProvider = modelProvider
	}
}

func WithModelName(modelName string) StartSpanOption {
	return func(c *illmobs.StartSpanConfig) {
		c.ModelName = modelName
	}
}

// ------------- Finish options -------------

type FinishSpanOption = func(cfg *illmobs.FinishSpanConfig)

// WithError marks the finished span with the given error.
func WithError(err error) FinishSpanOption {
	return func(cfg *illmobs.FinishSpanConfig) {
		cfg.Error = err
	}
}

// WithFinishTime allows to provide a custom finish time.
func WithFinishTime(t time.Time) FinishSpanOption {
	return func(cfg *illmobs.FinishSpanConfig) {
		cfg.FinishTime = t
	}
}

// ------------- Annotate options -------------

type AnnotateOption func(a *illmobs.SpanAnnotations)

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

func WithAnnotatedSessionID(sessionID string) AnnotateOption {
	return func(a *illmobs.SpanAnnotations) {
		if a.Tags == nil {
			a.Tags = make(map[string]string)
		}
		a.Tags[illmobs.TagKeySessionID] = sessionID
	}
}

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
