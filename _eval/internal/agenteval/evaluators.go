// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"fmt"

	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
	"github.com/DataDog/dd-trace-go/v2/llmobs/experiment"
)

var fixedCriteria = []string{
	CheckDiffNotEmpty,
}

var optionalCriteria = []string{
	CheckExpectedPathsTouched,
	CheckForbiddenPathsUntouched,
	CheckDiffWithinLimit,
	CheckRegisteredInPackagesGo,
	CheckOrchestrionAspect,
	CheckOrchestrionSchemaValid,
	CheckRequiredPathsPresent,
}

// Evaluators builds the stable columns for one task experiment. Task setup and
// scoring stay in Go, while the dataset contains only prompts and expected
// observable results.
func Evaluators(spec *TaskSpec, extra ...experiment.Evaluator) []experiment.Evaluator {
	evs := []experiment.Evaluator{
		ScoreEvaluator("checks_score", func(o *AgentRunOutput) float64 { return o.ChecksScore }),
		ScoreEvaluator("validation_score", func(o *AgentRunOutput) float64 { return o.ValidationScore }),
		ScoreEvaluator("diff_line_count", func(o *AgentRunOutput) float64 { return float64(o.DiffLineCount) }),
		ScoreEvaluator("duration_seconds", func(o *AgentRunOutput) float64 { return float64(o.DurationMillis) / 1000 }),
		ScoreEvaluator("docs_read_count", func(o *AgentRunOutput) float64 { return float64(len(o.DocsRead)) }),
		ScoreEvaluator("tool_calls", func(o *AgentRunOutput) float64 { return float64(o.ToolCalls) }),
	}
	return append(evs, extra...)
}

func ScoreEvaluator(name string, fn func(*AgentRunOutput) float64) experiment.Evaluator {
	return experiment.NewEvaluator(name, func(_ context.Context, _ dataset.Record, output any) (any, error) {
		if output == nil {
			return nil, nil
		}
		out, err := AsOutput(output)
		if err != nil {
			return nil, err
		}
		if out.Status == RunStatusInfrastructureFailure {
			return nil, nil
		}
		return fn(out), nil
	})
}

func AsOutput(output any) (*AgentRunOutput, error) {
	out, ok := output.(*AgentRunOutput)
	if !ok {
		return nil, fmt.Errorf("expected *AgentRunOutput, got %T", output)
	}
	if out == nil {
		return nil, fmt.Errorf("task returned a nil *AgentRunOutput")
	}
	return out, nil
}

func tally(values map[string]bool) (passed, total int, score float64) {
	for _, value := range values {
		total++
		if value {
			passed++
		}
	}
	if total > 0 {
		score = float64(passed) / float64(total)
	}
	return passed, total, score
}

func weightedTally(values, weights map[string]float64) (passed, total int, score float64) {
	var earned, possible float64
	for name, value := range values {
		weight := weights[name]
		if weight == 0 {
			weight = 1
		}
		total++
		if value == 1 {
			passed++
		}
		earned += value * weight
		possible += weight
	}
	if possible > 0 {
		score = earned / possible
	}
	return passed, total, score
}

func expectedChecks(spec *TaskSpec) map[string]float64 {
	out := make(map[string]float64, len(fixedCriteria)+len(optionalCriteria)+len(spec.SourceChecks))
	for _, name := range fixedCriteria {
		out[name] = 1
	}
	if len(spec.ExpectedChangedPaths) > 0 {
		out[CheckExpectedPathsTouched] = 1
	}
	if len(spec.ForbiddenPaths) > 0 {
		out[CheckForbiddenPathsUntouched] = 1
	}
	if spec.MaxDiffLines > 0 {
		out[CheckDiffWithinLimit] = 1
	}
	if spec.RegistrationImport != "" {
		out[CheckRegisteredInPackagesGo] = 1
	}
	if spec.OrchestrionYAML != "" {
		out[CheckOrchestrionAspect] = 1
		out[CheckOrchestrionSchemaValid] = 1
	}
	if len(spec.RequiredPaths) > 0 {
		out[CheckRequiredPathsPresent] = 1
	}
	for _, check := range spec.SourceChecks {
		out[check.Label] = 1
	}
	return out
}

func expectedDiagnostics(spec *TaskSpec) map[string]bool {
	out := map[string]bool{
		CheckAgentExitedOK:       true,
		CheckNoPermissionDenials: true,
	}
	if len(spec.DocsExpectedRead) > 0 {
		out[CheckDocsOpened] = true
	}
	if len(spec.UpstreamMarkers) > 0 {
		out[CheckNoUpstreamFetch] = true
	}
	return out
}
