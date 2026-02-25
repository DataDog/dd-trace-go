// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package experiment

import (
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/config"
	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
)

// ProgressStatus represents the status of a progress event during experiment execution.
type ProgressStatus string

const (
	ProgressRunning             ProgressStatus = "running"
	ProgressTaskComplete        ProgressStatus = "task_complete"
	ProgressEvaluationsComplete ProgressStatus = "evaluations_complete"
	ProgressSuccess             ProgressStatus = "success"
	ProgressError               ProgressStatus = "error"
)

// ProgressEvent represents a progress update during experiment execution.
type ProgressEvent struct {
	RecordIndex int
	Status      ProgressStatus
	Record      *dataset.Record
	Output      any
	Evaluations []*Evaluation
	Error       error
}

type newCfg struct {
	projectName       string
	description       string
	tags              map[string]string
	experimentCfg     map[string]any
	summaryEvaluators []SummaryEvaluator
}

func defaultNewCfg(globalCfg *config.Config) *newCfg {
	return &newCfg{
		projectName: globalCfg.ProjectName,
	}
}

type Option func(cfg *newCfg)

func WithProjectName(name string) Option {
	return func(cfg *newCfg) {
		cfg.projectName = name
	}
}

func WithTags(tags map[string]string) Option {
	return func(cfg *newCfg) {
		cfg.tags = tags
	}
}

func WithDescription(description string) Option {
	return func(cfg *newCfg) {
		cfg.description = description
	}
}

func WithExperimentConfig(experimentCfg map[string]any) Option {
	return func(cfg *newCfg) {
		cfg.experimentCfg = experimentCfg
	}
}

// WithSummaryEvaluators sets the summary evaluators for the experiment.
// Summary evaluators run after all tasks and evaluators have completed,
// receiving all experiment results to compute aggregate metrics.
func WithSummaryEvaluators(summaryEvaluators ...SummaryEvaluator) Option {
	return func(cfg *newCfg) {
		cfg.summaryEvaluators = summaryEvaluators
	}
}

type runCfg struct {
	maxConcurrency      int
	abortOnError        bool
	sampleSize          int
	progressCallback    func(ProgressEvent)
	onExperimentCreated func(id, name string)
}

func defaultRunCfg() *runCfg {
	return &runCfg{}
}

type RunOption func(cfg *runCfg)

func WithMaxConcurrency(maxConcurrency int) RunOption {
	return func(cfg *runCfg) {
		cfg.maxConcurrency = maxConcurrency
	}
}

func WithAbortOnError(abortOnError bool) RunOption {
	return func(cfg *runCfg) {
		cfg.abortOnError = abortOnError
	}
}

func WithSampleSize(sampleSize int) RunOption {
	return func(cfg *runCfg) {
		cfg.sampleSize = sampleSize
	}
}

// WithProgressCallback sets a callback that is invoked for each progress event
// during experiment execution. This enables real-time streaming of experiment
// progress (e.g., for the devserver).
func WithProgressCallback(fn func(ProgressEvent)) RunOption {
	return func(cfg *runCfg) {
		cfg.progressCallback = fn
	}
}

// WithOnExperimentCreated sets a callback that is invoked after the experiment
// is created on the backend, providing the experiment ID and run name.
func WithOnExperimentCreated(fn func(id, name string)) RunOption {
	return func(cfg *runCfg) {
		cfg.onExperimentCreated = fn
	}
}
