// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package experiment

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/errortrace"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
	"github.com/DataDog/dd-trace-go/v2/llmobs/internal"
	"github.com/DataDog/dd-trace-go/v2/llmobs/internal/transport"
)

var (
	errRequiresProjectName = errors.New(`a project name must be provided for the experiment, either configured via the DD_LLMOBS_PROJECT_NAME
environment variable, using the global llmobs.WithProjectName option, or experiment.WithProjectName option.`)
)

// Experiment represents a DataDog LLM Observability experiment.
type Experiment struct {
	Name string

	cfg         *newCfg
	task        Task
	dataset     *dataset.Dataset
	evaluators  []Evaluator
	description string
	tagsSlice   []string

	// these are set after the experiment is run
	id      string
	runName string
}

// Task represents the task to run for an Experiment.
type Task interface {
	Name() string
	Run(ctx context.Context, inputData map[string]any, experimentCfg map[string]any) (any, error)
}

// Evaluator represents an evaluator for an Experiment.
type Evaluator interface {
	Name() string
	Run(ctx context.Context, input map[string]any, output any, expectedOutput any) (any, error)
}

// TaskFunc is the type for Task functions.
type TaskFunc func(ctx context.Context, inputData map[string]any, experimentCfg map[string]any) (any, error)

type namedTask struct {
	name string
	fn   TaskFunc
}

func (n *namedTask) Name() string {
	return n.name
}

func (n *namedTask) Run(ctx context.Context, inputData map[string]any, experimentCfg map[string]any) (any, error) {
	return n.fn(ctx, inputData, experimentCfg)
}

// NewTask creates a new Task.
func NewTask(name string, fn TaskFunc) Task {
	return &namedTask{
		name: name,
		fn:   fn,
	}
}

// EvaluatorFunc is the type for Evaluator functions.
type EvaluatorFunc func(ctx context.Context, input map[string]any, output any, expectedOutput any) (any, error)

type namedEvaluator struct {
	name string
	fn   EvaluatorFunc
}

func (n *namedEvaluator) Name() string {
	return n.name
}

func (n *namedEvaluator) Run(ctx context.Context, input map[string]any, output any, expectedOutput any) (any, error) {
	return n.fn(ctx, input, output, expectedOutput)
}

// NewEvaluator creates a new Evaluator.
func NewEvaluator(name string, fn EvaluatorFunc) Evaluator {
	return &namedEvaluator{
		name: name,
		fn:   fn,
	}
}

// Result represents an experiment result.
type Result struct {
	RecordIndex    int
	SpanID         string
	TraceID        string
	Timestamp      time.Time
	Input          map[string]any
	Output         any
	ExpectedOutput any
	Evaluations    []*Evaluation
	Metadata       map[string]any
	Error          error
}

// Evaluation represents the output of an evaluator.
type Evaluation struct {
	Name  string
	Value any
	Error error
}

func New(name string, task Task, ds *dataset.Dataset, evaluators []Evaluator, description string, opts ...Option) (*Experiment, error) {
	llmobs, err := internal.ActiveLLMObs()
	if err != nil {
		return nil, err
	}

	cfg := defaultNewCfg(llmobs.Config)
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.projectName == "" {
		return nil, errRequiresProjectName
	}

	if cfg.tags == nil {
		cfg.tags = make(map[string]string)
	}
	cfg.tags["ddtrace.version"] = version.Tag

	tagsSlice := make([]string, 0, len(cfg.tags))
	for k, v := range cfg.tags {
		tagsSlice = append(tagsSlice, fmt.Sprintf("%s:%s", k, v))
	}

	return &Experiment{
		Name:        name,
		task:        task,
		dataset:     ds,
		evaluators:  evaluators,
		description: description,
		cfg:         cfg,
		tagsSlice:   tagsSlice,
	}, nil
}

func (e *Experiment) Run(ctx context.Context, opts ...RunOption) ([]*Result, error) {
	llmobs, err := internal.ActiveLLMObs()
	if err != nil {
		return nil, err
	}
	cfg := defaultRunCfg()
	for _, opt := range opts {
		opt(cfg)
	}

	// 1) Create or get the project
	proj, err := llmobs.Transport.ProjectGetOrCreate(ctx, e.cfg.projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create project: %w", err)
	}

	// 2) Create the experiment
	expResp, err := llmobs.Transport.ExperimentCreate(ctx, e.Name, e.dataset.ID(), proj.ID, e.dataset.Version(), e.cfg.experimentCfg, e.tagsSlice, e.description)
	if err != nil {
		return nil, fmt.Errorf("failed to create experiment: %w", err)
	}
	e.id = expResp.ID
	e.runName = expResp.Name

	pushEventsTags := make([]string, len(e.tagsSlice))
	copy(pushEventsTags, e.tagsSlice)
	pushEventsTags = append(pushEventsTags, fmt.Sprintf("%s:%s", "experiment_id", e.id))

	// 3) Run the experiment task for each record in the dataset
	results, err := e.runTask(ctx, llmobs, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to run experiment task: %w", err)
	}
	if err := e.runEvaluators(ctx, results, cfg); err != nil {
		return nil, fmt.Errorf("failed to run experiment evaluators: %w", err)
	}

	// 4) Generate and publish metrics from the results
	metrics := e.generateMetrics(results)
	if err := llmobs.Transport.ExperimentPushEvents(ctx, e.id, metrics, pushEventsTags); err != nil {
		return nil, fmt.Errorf("failed to push experiment events: %w", err)
	}

	return results, nil
}

func (e *Experiment) URL() string {
	// FIXME(rarguelloF): will not work for subdomain orgs
	return fmt.Sprintf("%s/llm/experiments/%s", internal.ResourceBaseURL(), e.id)
}

func (e *Experiment) runTask(ctx context.Context, llmobs *internal.LLMObs, cfg *runCfg) ([]*Result, error) {
	eg, ctx := errgroup.WithContext(ctx)
	if cfg.maxConcurrency > 0 {
		eg.SetLimit(cfg.maxConcurrency)
	}

	dsSize := e.dataset.Len()
	if cfg.sampleSize > 0 && cfg.sampleSize <= e.dataset.Len() {
		dsSize = cfg.sampleSize
	}
	results := make([]*Result, dsSize)

	for i, rec := range e.dataset.Records() {
		if cfg.sampleSize > 0 && i >= cfg.sampleSize {
			break
		}
		eg.Go(func() error {
			res := e.runTaskForRecord(ctx, llmobs, i, rec)
			if res.Error != nil {
				retErr := fmt.Errorf("failed to process record %d: %w", i, res.Error.Error)
				if cfg.abortOnError {
					return retErr
				} else {
					log.Warn("llmobs: %s", retErr)
				}
			}
			results[i] = res
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}
	// Ensure spans get submitted in serverless environments
	llmobs.Flush()
	return results, nil
}

func (e *Experiment) runTaskForRecord(ctx context.Context, llmobs *internal.LLMObs, recIdx int, rec dataset.Record) *Result {
	var (
		err       error
		startTime = time.Now()
	)

	span, ctx := llmobs.StartExperimentSpan(ctx, e.task.Name(), e.id, internal.WithStartTime(startTime))
	defer span.Finish(internal.WithError(err))

	tags := make(map[string]string)
	for k, v := range e.cfg.tags {
		tags[k] = v
	}
	tags["dataset_id"] = e.dataset.ID()
	tags["dataset_record_id"] = rec.ID()
	tags["experiment_id"] = e.id

	// TODO: context cancelation
	out, err := e.task.Run(ctx, rec.Input, e.cfg.experimentCfg)
	if err != nil {
		err = errortrace.Wrap(err)
	}

	llmobs.AnnotateExperimentSpan(span, internal.ExperimentSpanAnnotations{
		Input:          rec.Input,
		Output:         out,
		Tags:           tags,
		ExpectedOutput: rec.ExpectedOutput,
	})

	return &Result{
		RecordIndex:    recIdx,
		SpanID:         strconv.FormatUint(span.SpanID(), 10),
		TraceID:        span.TraceID(),
		Timestamp:      startTime,
		Input:          rec.Input,
		Output:         out,
		ExpectedOutput: rec.ExpectedOutput,
		Metadata: map[string]any{
			"dataset_record_index": recIdx,
			"experiment_name":      e.Name,
			"dataset_name":         e.dataset.Name(),
			"tags":                 e.tagsSlice,
		},
		Error: err,
	}
}

func (e *Experiment) runEvaluators(ctx context.Context, results []*Result, cfg *runCfg) error {
	eg, ctx := errgroup.WithContext(ctx)
	if cfg.maxConcurrency > 0 {
		eg.SetLimit(cfg.maxConcurrency)
	}

	for _, res := range results {
		rec, ok := e.dataset.Record(res.RecordIndex)
		if !ok {
			log.Warn("record %d not found in dataset", res.RecordIndex)
			continue
		}

		eg.Go(func() error {
			evs := make([]*Evaluation, 0, len(e.evaluators))
			for evIdx, ev := range e.evaluators {
				val, err := ev.Run(ctx, rec.Input, res.Output, rec.ExpectedOutput)
				if err != nil {
					// this error will be used later to create the payload sent to the backend, so it must contain the
					// stacktrace.
					err = errortrace.Wrap(err)
					retErr := fmt.Errorf("evaluator %d (%s) failed on record %d: %w", evIdx, ev.Name(), res.RecordIndex, err)
					if cfg.abortOnError {
						return retErr
					} else {
						log.Warn("llmobs: %s", retErr)
					}
				}
				evs = append(evs, &Evaluation{
					Name:  ev.Name(),
					Value: val,
					Error: err,
				})
			}
			res.Evaluations = evs
			return nil
		})
	}
	return eg.Wait()
}

func (e *Experiment) generateMetrics(results []*Result) []transport.ExperimentEvalMetricEvent {
	metrics := make([]transport.ExperimentEvalMetricEvent, 0, len(results))

	for _, res := range results {
		for _, ev := range res.Evaluations {
			metrics = append(metrics, e.generateMetricFromEvaluation(res, ev))
		}
	}
	return metrics
}

func (e *Experiment) generateMetricFromEvaluation(res *Result, ev *Evaluation) transport.ExperimentEvalMetricEvent {
	var (
		catVal   *string
		scoreVal *float64
		boolVal  *bool
	)

	metricType := "categorical"
	switch t := ev.Value.(type) {
	case bool:
		metricType = "boolean"
		boolVal = transport.AnyPtr(t)

	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, uintptr,
		float32, float64:
		metricType = "score"
		scoreVal = transport.AnyPtr(asFloat64(t))

	default:
		catVal = transport.AnyPtr(fmt.Sprintf("%v", t))
	}

	return transport.ExperimentEvalMetricEvent{
		SpanID:           res.SpanID,
		TraceID:          res.TraceID,
		TimestampMS:      res.Timestamp.UnixMilli(),
		MetricType:       metricType,
		Label:            ev.Name,
		CategoricalValue: catVal,
		ScoreValue:       scoreVal,
		BooleanValue:     boolVal,
		Error:            transport.NewErrorMessage(ev.Error),
		Tags:             e.tagsSlice,
		ExperimentID:     e.id,
	}
}

func asFloat64(x any) float64 {
	switch v := x.(type) {
	case float64:
		return v
	case float32:
		return float64(v)

	case int:
		return float64(v)
	case int8:
		return float64(v)
	case int16:
		return float64(v)
	case int32:
		return float64(v)
	case int64:
		return float64(v)

	case uint:
		return float64(v)
	case uint8:
		return float64(v)
	case uint16:
		return float64(v)
	case uint32:
		return float64(v)
	case uint64:
		return float64(v)
	case uintptr:
		return float64(v)
	}
	return 0
}
