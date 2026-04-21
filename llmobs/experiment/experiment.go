// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package experiment

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/errortrace"
	illmobs "github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs/transport"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
)

var (
	errRequiresProjectName = errors.New(`a project name must be provided for the experiment, either configured via the DD_LLMOBS_PROJECT_NAME
environment variable, using the global tracer.WithLLMObsProjectName option, or experiment.WithProjectName option`)
	errRequiresAppKey = errors.New(`an app key must be provided for the experiment in agentless mode configured via the DD_APP_KEY environment variable`)
)

const (
	experimentStatusRunning     = "running"
	experimentStatusCompleted   = "completed"
	experimentStatusFailed      = "failed"
	experimentStatusInterrupted = "interrupted"
)

// Experiment represents a DataDog LLM Observability experiment.
type Experiment struct {
	Name string

	cfg               *newCfg
	task              Task
	dataset           *dataset.Dataset
	evaluators        []Evaluator
	summaryEvaluators []SummaryEvaluator
	description       string
	tagsSlice         []string

	// these are set after the experiment is run
	id      string
	runName string
}

// Task represents the task to run for an Experiment.
type Task interface {
	Name() string
	Run(ctx context.Context, rec dataset.Record, experimentCfg map[string]any) (any, error)
}

// Evaluator represents an evaluator for an Experiment.
type Evaluator interface {
	Name() string
	Run(ctx context.Context, rec dataset.Record, output any) (any, error)
}

// TaskFunc is the type for Task functions.
type TaskFunc func(ctx context.Context, rec dataset.Record, experimentCfg map[string]any) (any, error)

type namedTask struct {
	name string
	fn   TaskFunc
}

func (n *namedTask) Name() string {
	return n.name
}

func (n *namedTask) Run(ctx context.Context, rec dataset.Record, experimentCfg map[string]any) (any, error) {
	return n.fn(ctx, rec, experimentCfg)
}

// NewTask creates a new Task.
func NewTask(name string, fn TaskFunc) Task {
	return &namedTask{
		name: name,
		fn:   fn,
	}
}

// EvaluatorFunc is the type for Evaluator functions.
type EvaluatorFunc func(ctx context.Context, rec dataset.Record, output any) (any, error)

type namedEvaluator struct {
	name string
	fn   EvaluatorFunc
}

func (n *namedEvaluator) Name() string {
	return n.name
}

func (n *namedEvaluator) Run(ctx context.Context, rec dataset.Record, output any) (any, error) {
	return n.fn(ctx, rec, output)
}

// NewEvaluator creates a new Evaluator.
func NewEvaluator(name string, fn EvaluatorFunc) Evaluator {
	return &namedEvaluator{
		name: name,
		fn:   fn,
	}
}

// SummaryEvaluator represents a summary evaluator for an Experiment.
// Summary evaluators run after all tasks and evaluators have completed,
// receiving all experiment results to compute aggregate metrics.
type SummaryEvaluator interface {
	Name() string
	Run(ctx context.Context, results []*RecordResult) (any, error)
}

// SummaryEvaluatorFunc is the type for SummaryEvaluator functions.
type SummaryEvaluatorFunc func(ctx context.Context, results []*RecordResult) (any, error)

type namedSummaryEvaluator struct {
	name string
	fn   SummaryEvaluatorFunc
}

func (n *namedSummaryEvaluator) Name() string {
	return n.name
}

func (n *namedSummaryEvaluator) Run(ctx context.Context, results []*RecordResult) (any, error) {
	return n.fn(ctx, results)
}

// NewSummaryEvaluator creates a new SummaryEvaluator.
func NewSummaryEvaluator(name string, fn SummaryEvaluatorFunc) SummaryEvaluator {
	return &namedSummaryEvaluator{
		name: name,
		fn:   fn,
	}
}

// RunInfo contains metadata for a single experiment run iteration.
type RunInfo struct {
	ID        string // UUID uniquely identifying this run
	Iteration int    // 1-indexed iteration number
}

// RunResult contains the results for a single run iteration.
type RunResult struct {
	Run                RunInfo
	Results            []*RecordResult
	SummaryEvaluations []*Evaluation
}

// ExperimentResult represents the complete results of an experiment execution.
// For multi-run experiments (WithRuns > 1), Runs contains one entry per iteration.
type ExperimentResult struct {
	// ExperimentName is the name of the experiment as provided to New.
	ExperimentName string
	// DatasetName is the name of the dataset the experiment ran against.
	DatasetName string
	// Runs holds the results for each run iteration, in order. For a single-run
	// experiment this slice has exactly one element.
	Runs []*RunResult
	// Results is kept for single-run backward compatibility and points to Runs[0].Results.
	//
	// Deprecated: Use Runs[0].Results instead.
	Results []*RecordResult
	// SummaryEvaluations is kept for single-run backward compatibility and points to Runs[0].SummaryEvaluations.
	//
	// Deprecated: Use Runs[0].SummaryEvaluations instead.
	SummaryEvaluations []*Evaluation
}

// RecordResult represents an experiment result for a single record.
type RecordResult struct {
	// Record is the dataset record containing input, expected output, and metadata.
	Record *dataset.Record
	// Output is the task output for this record.
	Output any
	// Evaluations holds the evaluation results for this record.
	Evaluations []*Evaluation

	// RecordIndex is the index of the record in the dataset.
	RecordIndex int
	// SpanID is the span ID for tracing.
	SpanID string
	// TraceID is the trace ID for tracing.
	TraceID string
	// Timestamp is when the task was executed.
	Timestamp time.Time
	// Error is any error that occurred during task execution.
	Error error
}

// Evaluation represents the output of an evaluator.
type Evaluation struct {
	Name  string
	Value any
	Error error
}

// New creates a new Experiment.
func New(name string, task Task, ds *dataset.Dataset, evaluators []Evaluator, opts ...Option) (*Experiment, error) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		return nil, err
	}

	cfg := defaultNewCfg(ll.Config)
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.projectName == "" {
		return nil, errRequiresProjectName
	}
	if ll.Config.ResolvedAgentlessEnabled && ll.Config.TracerConfig.APPKey == "" {
		return nil, errRequiresAppKey
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
		Name:              name,
		task:              task,
		dataset:           ds,
		evaluators:        evaluators,
		summaryEvaluators: cfg.summaryEvaluators,
		description:       cfg.description,
		cfg:               cfg,
		tagsSlice:         tagsSlice,
	}, nil
}

// Run executes the experiment, running the task and evaluators on each record in the dataset,
// then running summary evaluators on the aggregated results.
// When configured with WithRuns(n), the full experiment loop is executed n times.
func (e *Experiment) Run(ctx context.Context, opts ...RunOption) (result *ExperimentResult, retErr error) {
	ll, err := illmobs.ActiveLLMObs()
	if err != nil {
		return nil, err
	}
	cfg := defaultRunCfg()
	for _, opt := range opts {
		opt(cfg)
	}

	// 1) Create or get the project
	proj, err := ll.Transport.GetOrCreateProject(ctx, e.cfg.projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create project: %w", err)
	}

	// 2) Create the experiment, telling the backend how many runs to expect
	expResp, err := ll.Transport.CreateExperiment(ctx, e.Name, e.dataset.ID(), proj.ID, e.dataset.Version(), e.cfg.experimentCfg, e.tagsSlice, e.description, e.cfg.runs)
	if err != nil {
		return nil, fmt.Errorf("failed to create experiment: %w", err)
	}
	e.id = expResp.ID
	e.runName = expResp.Name

	// 3) Notify the backend that the experiment is now executing.
	e.updateStatus(ctx, ll, experimentStatusRunning, "")

	result = &ExperimentResult{
		ExperimentName: e.Name,
		DatasetName:    e.dataset.Name(),
		Runs:           make([]*RunResult, 0, e.cfg.runs),
	}

	// Ensure we send the final status in all cases.
	defer func() {
		if retErr != nil {
			if ctx.Err() != nil {
				e.updateStatus(context.Background(), ll, experimentStatusInterrupted, "")
			} else {
				e.updateStatus(ctx, ll, experimentStatusFailed, retErr.Error())
			}
			return
		}
		if summary := buildErrorSummary(result.Runs); summary != "" {
			e.updateStatus(ctx, ll, experimentStatusFailed, summary)
		} else {
			e.updateStatus(ctx, ll, experimentStatusCompleted, "")
		}
	}()

	for i := range e.cfg.runs {
		run := RunInfo{
			ID:        uuid.New().String(),
			Iteration: i + 1,
		}
		// Build metric-level tags (per-metric Tags field)
		metricTags := e.buildRunTags(run, false)
		// Build request-level tags (outer tags on PushExperimentEvents)
		pushTags := e.buildRunTags(run, true)

		// 4) Run the experiment task for each record in the dataset
		results, err := e.runTask(ctx, ll, cfg, run)
		if err != nil {
			return nil, fmt.Errorf("run %d: failed to run experiment task: %w", run.Iteration, err)
		}
		if err := e.runEvaluators(ctx, results, cfg); err != nil {
			return nil, fmt.Errorf("run %d: failed to run experiment evaluators: %w", run.Iteration, err)
		}

		// 5) Run summary evaluators
		summaryEvals, err := e.runSummaryEvaluators(ctx, results, cfg)
		if err != nil {
			return nil, fmt.Errorf("run %d: failed to run summary evaluators: %w", run.Iteration, err)
		}

		// 6) Generate and publish metrics from the results
		metrics := e.generateMetrics(results, summaryEvals, metricTags)
		if err := ll.Transport.PushExperimentEvents(ctx, e.id, metrics, pushTags); err != nil {
			return nil, fmt.Errorf("run %d: failed to push experiment events: %w", run.Iteration, err)
		}

		result.Runs = append(result.Runs, &RunResult{
			Run:                run,
			Results:            results,
			SummaryEvaluations: summaryEvals,
		})
	}

	// Populate legacy fields from the first run for single-run backward compatibility.
	if len(result.Runs) > 0 {
		result.Results = result.Runs[0].Results
		result.SummaryEvaluations = result.Runs[0].SummaryEvaluations
	}

	return
}

// updateStatus sends a status update to the backend. Failures are logged at debug
// level and never surfaced to the caller — a status update failure is non-fatal.
func (e *Experiment) updateStatus(ctx context.Context, ll *illmobs.LLMObs, status, errSummary string) {
	if e.id == "" {
		return
	}
	if err := ll.Transport.UpdateExperimentStatus(ctx, e.id, status, errSummary); err != nil {
		log.Debug("llmobs: failed to update experiment %s status to %q: %v", e.id, status, err.Error())
	}
}

// buildErrorSummary returns a semicolon-separated string of all task and evaluator
// errors found across all run results. An empty string means no errors occurred.
func buildErrorSummary(runs []*RunResult) string {
	var parts []string
	for _, run := range runs {
		for _, res := range run.Results {
			if res == nil {
				continue
			}
			if res.Error != nil {
				parts = append(parts, res.Error.Error())
			}
			for _, ev := range res.Evaluations {
				if ev.Error != nil {
					parts = append(parts, fmt.Sprintf("%s: %s", ev.Name, ev.Error.Error()))
				}
			}
		}
		for _, ev := range run.SummaryEvaluations {
			if ev.Error != nil {
				parts = append(parts, fmt.Sprintf("%s: %s", ev.Name, ev.Error.Error()))
			}
		}
	}
	return strings.Join(parts, "; ")
}

// buildRunTags returns the tag slice for a given run, optionally including experiment_id.
// includeExperimentID should be true for the outer PushExperimentEvents tags, false for
// the per-metric Tags field (which carries the experiment ID in its own struct field).
func (e *Experiment) buildRunTags(run RunInfo, includeExperimentID bool) []string {
	tags := make([]string, 0, len(e.tagsSlice)+3)
	tags = append(tags, e.tagsSlice...)
	tags = append(tags,
		fmt.Sprintf("run_id:%s", run.ID),
		fmt.Sprintf("run_iteration:%d", run.Iteration),
	)
	if includeExperimentID {
		tags = append(tags, fmt.Sprintf("experiment_id:%s", e.id))
	}
	return tags
}

func (e *Experiment) URL() string {
	// FIXME(rarguelloF): will not work for subdomain orgs
	return fmt.Sprintf("%s/llm/experiments/%s", illmobs.PublicResourceBaseURL(), e.id)
}

func (e *Experiment) runTask(ctx context.Context, llmobs *illmobs.LLMObs, cfg *runCfg, run RunInfo) ([]*RecordResult, error) {
	eg, ctx := errgroup.WithContext(ctx)
	if cfg.maxConcurrency > 0 {
		eg.SetLimit(cfg.maxConcurrency)
	}

	dsSize := e.dataset.Len()
	if cfg.sampleSize > 0 && cfg.sampleSize <= e.dataset.Len() {
		dsSize = cfg.sampleSize
	}
	results := make([]*RecordResult, dsSize)

	for i, rec := range e.dataset.Records() {
		if cfg.sampleSize > 0 && i >= cfg.sampleSize {
			break
		}
		eg.Go(func() error {
			res := e.runTaskForRecord(ctx, llmobs, i, rec, run)
			if res.Error != nil {
				retErr := fmt.Errorf("failed to process record %d: %w", i, res.Error)
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

func (e *Experiment) runTaskForRecord(ctx context.Context, llmobs *illmobs.LLMObs, recIdx int, rec dataset.Record, run RunInfo) *RecordResult {
	var (
		err error
	)

	span, ctx := llmobs.StartExperimentSpan(ctx, e.task.Name(), illmobs.ExperimentInfo{
		ID:           e.id,
		RunID:        run.ID,
		RunIteration: run.Iteration,
	}, illmobs.StartSpanConfig{})
	defer func() { span.Finish(illmobs.FinishSpanConfig{Error: err}) }()

	tags := make(map[string]string)
	maps.Copy(tags, e.cfg.tags)
	tags["dataset_id"] = e.dataset.ID()
	tags["dataset_record_id"] = rec.ID()
	tags["experiment_id"] = e.id
	tags["run_id"] = run.ID
	tags["run_iteration"] = fmt.Sprintf("%d", run.Iteration)

	out, err := e.task.Run(ctx, rec, e.cfg.experimentCfg)
	if err != nil {
		err = errortrace.Wrap(err)
	}

	span.Annotate(illmobs.SpanAnnotations{
		ExperimentInput:          rec.Input,
		ExperimentOutput:         out,
		ExperimentExpectedOutput: rec.ExpectedOutput,
		Tags:                     tags,
	})

	return &RecordResult{
		Record:      &rec,
		Output:      out,
		RecordIndex: recIdx,
		SpanID:      span.SpanID(),
		TraceID:     span.TraceID(),
		Timestamp:   span.StartTime(),
		Error:       err,
	}
}

func (e *Experiment) runEvaluators(ctx context.Context, results []*RecordResult, cfg *runCfg) error {
	eg, ctx := errgroup.WithContext(ctx)
	if cfg.maxConcurrency > 0 {
		eg.SetLimit(cfg.maxConcurrency)
	}

	for _, res := range results {
		eg.Go(func() error {
			evs := make([]*Evaluation, 0, len(e.evaluators))
			for evIdx, ev := range e.evaluators {
				val, err := ev.Run(ctx, *res.Record, res.Output)
				if err != nil {
					// this error will be used later to create the payload sent to the backend, so it must contain the
					// stacktrace.
					err = errortrace.Wrap(err)
					retErr := fmt.Errorf("evaluator %d (%s) failed on record %s: %w", evIdx, ev.Name(), res.Record.ID(), err)
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

func (e *Experiment) runSummaryEvaluators(ctx context.Context, results []*RecordResult, cfg *runCfg) ([]*Evaluation, error) {
	if len(e.summaryEvaluators) == 0 {
		return nil, nil
	}

	// Run summary evaluators
	summaryEvals := make([]*Evaluation, 0, len(e.summaryEvaluators))
	for evIdx, sumEv := range e.summaryEvaluators {
		val, err := sumEv.Run(ctx, results)
		if err != nil {
			// Wrap error with stacktrace for backend
			err = errortrace.Wrap(err)
			retErr := fmt.Errorf("summary evaluator %d (%s) failed: %w", evIdx, sumEv.Name(), err)
			if cfg.abortOnError {
				return nil, retErr
			} else {
				log.Warn("llmobs: %s", retErr)
			}
		}
		summaryEvals = append(summaryEvals, &Evaluation{
			Name:  sumEv.Name(),
			Value: val,
			Error: err,
		})
	}

	return summaryEvals, nil
}

func (e *Experiment) generateMetrics(results []*RecordResult, summaryEvals []*Evaluation, runTags []string) []transport.ExperimentEvalMetricEvent {
	metrics := make([]transport.ExperimentEvalMetricEvent, 0, len(results)+len(summaryEvals))

	// Track latest timestamp for summary evaluations
	var latestTimestamp time.Time

	// Generate metrics from per-record evaluations
	for _, res := range results {
		if res.Timestamp.After(latestTimestamp) {
			latestTimestamp = res.Timestamp
		}
		for _, ev := range res.Evaluations {
			metrics = append(metrics, e.generateMetricFromEvaluation(res, ev, "custom", runTags))
		}
	}

	// Generate metrics from summary evaluations.
	// Summary evaluations don't have associated spans, so we use empty span/trace IDs and latest timestamp.
	for _, sumEv := range summaryEvals {
		metrics = append(metrics, e.generateMetricFromSummaryEvaluation(sumEv, latestTimestamp, runTags))
	}
	return metrics
}

func (e *Experiment) generateMetricFromEvaluation(res *RecordResult, ev *Evaluation, source string, runTags []string) transport.ExperimentEvalMetricEvent {
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
		MetricSource:     source,
		SpanID:           res.SpanID,
		TraceID:          res.TraceID,
		TimestampMS:      res.Timestamp.UnixMilli(),
		MetricType:       metricType,
		Label:            ev.Name,
		CategoricalValue: catVal,
		ScoreValue:       scoreVal,
		BooleanValue:     boolVal,
		Error:            transport.NewErrorMessage(ev.Error),
		Tags:             runTags,
		ExperimentID:     e.id,
	}
}

func (e *Experiment) generateMetricFromSummaryEvaluation(ev *Evaluation, timestamp time.Time, runTags []string) transport.ExperimentEvalMetricEvent {
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

	// Summary evaluations don't have span/trace IDs, but use the latest timestamp from per-record evaluations.
	return transport.ExperimentEvalMetricEvent{
		MetricSource:     "summary",
		SpanID:           "",
		TraceID:          "",
		TimestampMS:      timestamp.UnixMilli(),
		MetricType:       metricType,
		Label:            ev.Name,
		CategoricalValue: catVal,
		ScoreValue:       scoreVal,
		BooleanValue:     boolVal,
		Error:            transport.NewErrorMessage(ev.Error),
		Tags:             runTags,
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
