// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/dd-trace-go/v2/llmobs"
	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
	"github.com/DataDog/dd-trace-go/v2/llmobs/experiment"
)

type ComparisonConfig struct {
	RepoDir      string
	MutationsDir string
	ResultsDir   string

	ProjectName  string
	SuiteName    string
	TaskDatasets map[string]DatasetPin
	Specs        []*TaskSpec

	ExtraEvaluators []experiment.Evaluator
	Label           string

	BaselineRef   string
	BaselineName  string
	CandidateRef  string
	CandidateName string

	Runner Runner

	Runs        int
	Concurrency int
	Tasks       []string

	AgentTimeout      time.Duration
	ValidationTimeout time.Duration
	KeepWorkspaces    bool
}

// DatasetPin identifies the latest synchronized dataset version for one task.
type DatasetPin struct {
	Name    string
	Version int
}

type TaskExperiment struct {
	TaskID string
	Side   Side
	Branch string
	URL    string
	Result *experiment.ExperimentResult
}

type Comparison struct {
	Experiments []*TaskExperiment
}

func RunComparison(ctx context.Context, cfg ComparisonConfig) (*Comparison, error) {
	if cfg.Runner == nil {
		return nil, fmt.Errorf("runner is required")
	}
	if cfg.Runs <= 0 {
		cfg.Runs = 1
	}

	specs, err := selectedSpecs(cfg.Specs, cfg.Tasks)
	if err != nil {
		return nil, err
	}

	runStarted := time.Now()
	totalArms := len(specs) * 2
	progressf("plan: suite %s, %d task(s), 2 sides, so %d arm(s)", cfg.SuiteName, len(specs), totalArms)
	progressf("plan: baseline %s (%s) against candidate %s (%s), agent %s (%s), effort %s",
		cfg.BaselineName, cfg.BaselineRef, cfg.CandidateName, cfg.CandidateRef,
		cfg.Runner.Name(), cfg.Runner.Model(), cfg.Runner.Effort())
	// One arm runs one agent session per prompt variant per run, and a task
	// carries several variants, so the session count is a multiple of the arm
	// count rather than equal to it. The variant count comes from the dataset,
	// which each arm pulls below.
	progressf("plan: each arm runs one session per prompt variant, %d run(s) per variant, timeout %s per session",
		cfg.Runs, fmtDuration(cfg.AgentTimeout))

	preflightStarted := time.Now()
	progressf("preflight: checking mutations apply at both refs")
	for _, ref := range []string{cfg.BaselineRef, cfg.CandidateRef} {
		if err := VerifyMutations(ctx, cfg.RepoDir, cfg.MutationsDir, ref, specs); err != nil {
			return nil, fmt.Errorf("preflight: %w", err)
		}
	}
	progressf("preflight: ok in %s", fmtDuration(time.Since(preflightStarted)))

	cmp := &Comparison{}
	sides := []struct {
		side   Side
		ref    string
		branch string
	}{
		{SideBaseline, cfg.BaselineRef, cfg.BaselineName},
		{SideCandidate, cfg.CandidateRef, cfg.CandidateName},
	}
	arm := 0
	for _, spec := range specs {
		pin, ok := cfg.TaskDatasets[spec.TaskID]
		if !ok {
			return cmp, fmt.Errorf("task %q has no synchronized dataset", spec.TaskID)
		}
		for _, target := range sides {
			arm++
			taskDataset, err := pullDataset(ctx, cfg.ProjectName, pin)
			if err != nil {
				return cmp, err
			}
			if err := keepTaskRecords(taskDataset, spec.TaskID); err != nil {
				return cmp, err
			}
			armStarted := time.Now()
			sessions := taskDataset.Len() * cfg.Runs
			progressf("arm %d/%d: task %s on %s (%s), %d prompt variant(s) x %d run(s) = %d agent session(s), up to %s each",
				arm, totalArms, spec.TaskID, target.side, target.branch,
				taskDataset.Len(), cfg.Runs, sessions, fmtDuration(cfg.AgentTimeout))
			result, err := runTaskExperiment(ctx, cfg, taskDataset, spec, target.side, target.ref, target.branch)
			if err != nil {
				progressf("arm %d/%d: failed after %s: %v", arm, totalArms, fmtDuration(time.Since(armStarted)), err)
				return cmp, fmt.Errorf("task %s on %s: %w", spec.TaskID, target.side, err)
			}
			progressf("arm %d/%d: task %s on %s finished in %s, elapsed %s of the comparison, results at %s",
				arm, totalArms, spec.TaskID, target.side, fmtDuration(time.Since(armStarted)),
				fmtDuration(time.Since(runStarted)), result.URL)
			cmp.Experiments = append(cmp.Experiments, result)
		}
	}
	progressf("comparison complete: %d arm(s) in %s", len(cmp.Experiments), fmtDuration(time.Since(runStarted)))
	return cmp, nil
}

func pullDataset(ctx context.Context, projectName string, pin DatasetPin) (*dataset.Dataset, error) {
	opts := []dataset.PullOption{dataset.WithPullVersion(pin.Version)}
	if projectName != "" {
		opts = append(opts, dataset.WithPullProjectName(projectName))
	}
	ds, err := dataset.Pull(ctx, pin.Name, opts...)
	if err != nil {
		return nil, fmt.Errorf("pull dataset %q version %d: %w", pin.Name, pin.Version, err)
	}
	return ds, nil
}

func runTaskExperiment(ctx context.Context, cfg ComparisonConfig, ds *dataset.Dataset, spec *TaskSpec, side Side, ref, branch string) (*TaskExperiment, error) {
	runner := &TaskRunner{
		RepoDir:           cfg.RepoDir,
		MutationsDir:      cfg.MutationsDir,
		ResultsDir:        cfg.ResultsDir,
		Branch:            branch,
		Runner:            cfg.Runner,
		AgentTimeout:      cfg.AgentTimeout,
		ValidationTimeout: cfg.ValidationTimeout,
		KeepWorkspaces:    cfg.KeepWorkspaces,
	}

	task := experiment.NewTask(spec.TaskID, func(ctx context.Context, rec dataset.Record, _ map[string]any) (any, error) {
		input, err := DecodeTaskInput(rec)
		if err != nil {
			return nil, err
		}
		runSpec := *spec
		runSpec.Prompt = input.Prompt

		span, agentCtx := llmobs.StartLLMSpan(ctx, cfg.Runner.Name()+".coding_session",
			llmobs.WithModelName(cfg.Runner.Model()),
			llmobs.WithModelProvider(runnerProvider(cfg.Runner)),
		)
		out, runErr := runner.RunCase(agentCtx, &runSpec, ref, side, input.PromptID)
		spanInput := []llmobs.LLMMessage{{Role: "user", Content: input.Prompt}}
		var spanOutput []llmobs.LLMMessage
		annotationOptions := []llmobs.AnnotateOption{
			llmobs.WithAnnotatedTags(map[string]string{
				"agent": cfg.Runner.Name(), "task_id": spec.TaskID,
				"prompt_id": input.PromptID, "side": string(side), "branch": branch,
			}),
			llmobs.WithAnnotatedMetadata(map[string]any{
				"git_ref": ref, "comparison": cfg.Label,
			}),
		}
		if out != nil {
			if out.FinalMessage != "" {
				spanOutput = []llmobs.LLMMessage{{Role: "assistant", Content: out.FinalMessage}}
			}
			annotationOptions = append(annotationOptions, llmobs.WithAnnotatedMetrics(tokenMetrics(out)))
		}
		span.AnnotateLLMIO(spanInput, spanOutput, annotationOptions...)
		if runErr != nil {
			span.Finish(llmobs.WithError(runErr))
			return nil, runErr
		}
		span.Finish()
		return out, nil
	})

	experimentName := taskExperimentName(spec, cfg.Runner.Name(), side, branch, cfg.Label)
	exp, err := experiment.New(
		experimentName,
		task,
		ds,
		Evaluators(spec, cfg.ExtraEvaluators...),
		experiment.WithProjectName(cfg.ProjectName),
		experiment.WithDescription(taskExperimentDescription(spec, cfg.Runner.Name(), cfg.Runner.Model(), cfg.Runner.Effort(), branch)),
		experiment.WithExperimentConfig(map[string]any{
			"suite": cfg.SuiteName, "task_id": spec.TaskID,
			"side": side, "branch": branch, "ref": ref,
			"agent": cfg.Runner.Name(), "model": cfg.Runner.Model(), "effort": cfg.Runner.Effort(),
			"agent_cli_version": cfg.Runner.Version(ctx),
			"dataset_version":   ds.Version(), "runs": cfg.Runs,
		}),
		experiment.WithTags(map[string]string{
			"agent": cfg.Runner.Name(), "model": cfg.Runner.Model(), "effort": cfg.Runner.Effort(),
			"label": cfg.Label, "suite": cfg.SuiteName, "task_id": spec.TaskID,
			"side": string(side), "branch": branch,
			"dataset_version": strconv.Itoa(ds.Version()),
		}),
		experiment.WithRuns(cfg.Runs),
	)
	if err != nil {
		return nil, err
	}

	options := []experiment.RunOption{}
	if cfg.Concurrency > 0 {
		options = append(options, experiment.WithMaxConcurrency(cfg.Concurrency))
	}
	result, err := exp.Run(ctx, options...)
	if err != nil {
		return nil, err
	}
	return &TaskExperiment{TaskID: spec.TaskID, Side: side, Branch: branch, URL: exp.URL(), Result: result}, nil
}

func selectedSpecs(authored []*TaskSpec, tasks []string) ([]*TaskSpec, error) {
	byID := make(map[string]*TaskSpec, len(authored))
	for _, spec := range authored {
		byID[spec.TaskID] = spec
	}
	for _, taskID := range tasks {
		if _, ok := byID[taskID]; !ok {
			return nil, fmt.Errorf("unknown task %q", taskID)
		}
	}
	var specs []*TaskSpec
	for _, spec := range authored {
		if !selected(tasks, spec.TaskID) {
			continue
		}
		specs = append(specs, spec)
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("no records match the requested tasks %v", tasks)
	}
	return specs, nil
}

func keepTaskRecords(ds *dataset.Dataset, taskID string) error {
	seen := make(map[string]struct{})
	for i := ds.Len() - 1; i >= 0; i-- {
		rec, ok := ds.Record(i)
		if !ok {
			return fmt.Errorf("dataset record %d disappeared", i)
		}
		input, err := DecodeTaskInput(rec)
		if err != nil {
			return fmt.Errorf("record %d (%s): %w", i, rec.ID(), err)
		}
		if input.TaskID != taskID {
			ds.Delete(i)
			continue
		}
		if _, duplicate := seen[input.PromptID]; duplicate {
			return fmt.Errorf("task %q has duplicate prompt %q", taskID, input.PromptID)
		}
		seen[input.PromptID] = struct{}{}
	}
	if len(seen) == 0 {
		return fmt.Errorf("dataset has no records for task %q", taskID)
	}
	return nil
}

func runnerProvider(runner Runner) string {
	if runner.Name() == "claude" {
		return "anthropic"
	}
	return "openai"
}

func tokenMetrics(out *AgentRunOutput) map[string]float64 {
	return map[string]float64{
		llmobs.MetricKeyInputTokens:           float64(out.InputTokens),
		llmobs.MetricKeyOutputTokens:          float64(out.OutputTokens),
		llmobs.MetricKeyTotalTokens:           float64(out.TokenCount),
		llmobs.MetricKeyCacheReadInputTokens:  float64(out.CachedInputTokens),
		llmobs.MetricKeyCacheWriteInputTokens: float64(out.CacheWriteInputTokens),
		llmobs.MetricKeyReasoningOutputTokens: float64(out.ReasoningOutputTokens),
	}
}

func taskExperimentDescription(spec *TaskSpec, agent, model, effort, branch string) string {
	return fmt.Sprintf("Task: %s. Branch: %s. Agent: %s. Model: %s. Effort: %s.", spec.TaskID, branch, agent, model, effort)
}

var invalidExperimentSuffix = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func experimentSuffix(branch string) string {
	branch = strings.TrimPrefix(branch, "refs/heads/")
	branch = strings.TrimPrefix(branch, "refs/remotes/")
	branch = strings.TrimPrefix(branch, "origin/")
	branch = strings.Trim(invalidExperimentSuffix.ReplaceAllString(branch, "-"), "-.")
	if branch == "" {
		return "unknown"
	}
	return branch
}

func taskExperimentName(spec *TaskSpec, agent string, side Side, branch, comparisonLabel string) string {
	name := spec.ExperimentName
	if name == "" {
		name = spec.TaskID
	}
	suffix := experimentSuffix(branch)
	if side == SideCandidate && comparisonLabel != "" {
		suffix = experimentSuffix(comparisonLabel)
	}
	return name + "-" + experimentSuffix(agent) + "-" + suffix
}

func selected(tasks []string, taskID string) bool {
	if len(tasks) == 0 {
		return true
	}
	return slicesContains(tasks, taskID)
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
