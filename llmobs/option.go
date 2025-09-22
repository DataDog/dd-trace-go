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

type (
	// StartLLMSpanOption defines options for StartLLMSpan
	StartLLMSpanOption = interface {
		StartSpanOptionLLM
	}
	// StartWorkflowSpanOption defines options for StartWorkflowSpan
	StartWorkflowSpanOption = interface {
		StartSpanOptionCommon
	}
	// StartAgentSpanOption defines options for StartAgentSpan
	StartAgentSpanOption = interface {
		StartSpanOptionCommon
	}
	// StartToolSpanOption defines options for StartToolSpan
	StartToolSpanOption = interface {
		StartSpanOptionCommon
	}
	// StartTaskSpanOption defines options for StartTaskSpan
	StartTaskSpanOption = interface {
		StartSpanOptionCommon
	}
	// StartEmbeddingSpanOption defines options for StartEmbeddingSpan
	StartEmbeddingSpanOption = interface {
		StartSpanOptionLLM
	}
	// StartRetrievalSpanOption defines options for StartRetrievalSpan
	StartRetrievalSpanOption = interface {
		StartSpanOptionCommon
	}
)

type commonConfig struct {
	sessionID string
	mlApp     string
	startTime time.Time
}

func (c *commonConfig) startSpanConfig() illmobs.StartSpanConfig {
	return illmobs.StartSpanConfig{
		SessionID: c.sessionID,
		MLApp:     c.mlApp,
		StartTime: c.startTime,
	}
}

type modelConfig struct {
	modelName     string
	modelProvider string
}

type llmConfig struct {
	commonConfig
	modelConfig
}

func (c *llmConfig) startSpanConfig() illmobs.StartSpanConfig {
	if c.modelProvider == "" {
		c.modelProvider = "custom"
	}
	if c.modelName == "" {
		c.modelName = "custom"
	}
	return illmobs.StartSpanConfig{
		SessionID:     c.sessionID,
		ModelName:     c.modelName,
		ModelProvider: c.modelProvider,
		MLApp:         c.mlApp,
		StartTime:     c.startTime,
	}
}

func WithSessionID(sessionID string) StartSpanOptionCommon {
	return startOptionCommonFunc(func(c *commonConfig) {
		c.sessionID = sessionID
	})
}

func WithMLApp(mlApp string) StartSpanOptionCommon {
	return startOptionCommonFunc(func(c *commonConfig) {
		c.mlApp = mlApp
	})
}

func WithStartTime(t time.Time) StartSpanOptionCommon {
	return startOptionCommonFunc(func(c *commonConfig) {
		c.startTime = t
	})
}

func WithModelProvider(modelProvider string) StartSpanOptionLLM {
	return startOptionLLMFunc(func(c *llmConfig) {
		c.modelProvider = modelProvider
	})
}

func WithModelName(modelName string) StartSpanOptionLLM {
	return startOptionLLMFunc(func(c *llmConfig) {
		c.modelName = modelName
	})
}

type StartSpanOptionCommon interface {
	applyCommon(*commonConfig)
	applyLLM(*llmConfig)
}

type StartSpanOptionLLM interface {
	applyLLM(*llmConfig)
}

type startOptionCommonFunc func(config *commonConfig)

func (f startOptionCommonFunc) applyCommon(cfg *commonConfig) { f(cfg) }

func (f startOptionCommonFunc) applyLLM(cfg *llmConfig) { f(&cfg.commonConfig) }

type startOptionLLMFunc func(*llmConfig)

func (f startOptionLLMFunc) applyLLM(cfg *llmConfig) { f(cfg) }

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
