// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package suites holds the eval task suites as reviewable Go code.
//
// A suite is a set of tasks that share a question. "Does this change make an
// agent better at authoring integrations" is one suite; "better at adding
// tests", "better at fixing a flaky test", "better at adding a module" are
// others. Each task has a separate dataset in LLM Obs.
//
// To add one, create a file here that calls Register from an init function. See
// README.md for what makes a task worth adding.
package suites

import (
	"fmt"
	"slices"
	"sort"

	"github.com/DataDog/dd-trace-go/_eval/internal/agenteval"
	"github.com/DataDog/dd-trace-go/v2/llmobs/dataset"
	"github.com/DataDog/dd-trace-go/v2/llmobs/experiment"
)

// Suite is one question, asked as a set of tasks.
type Suite struct {
	// Name identifies the suite on the command line and tags every experiment.
	Name string
	// Dataset is the prefix for this suite's per-task LLM Obs datasets.
	Dataset string
	// Description explains what the suite measures. It becomes the dataset
	// description in LLM Obs.
	Description string
	// Docs are the paths whose effect the suite is built to detect. This is
	// documentation for whoever reads the suite next, not something scored;
	// scoring is per task, via TaskSpec.DocsExpectedRead.
	Docs []string
	// Each task and git revision becomes an experiment. Prompt variants become
	// the dataset records shown inside it.
	Tasks []agenteval.Task
	// Evaluators contributes criteria that cannot be expressed as task data.
	// Most suites need none: validation commands and source checks cover the
	// usual cases. Build numeric metrics with agenteval.ScoreEvaluator.
	Evaluators []experiment.Evaluator
}

// Records renders the suite for pushing to LLM Obs.
func (s *Suite) Records() ([]dataset.Record, error) {
	var out []dataset.Record
	for i, task := range s.Tasks {
		records, err := task.Records(s.Name)
		if err != nil {
			return nil, fmt.Errorf("suite %s, task %d: %w", s.Name, i, err)
		}
		out = append(out, records...)
	}
	return out, nil
}

// DatasetName returns the stable LLM Obs dataset name for one task.
func (s *Suite) DatasetName(taskID string) string {
	return TaskDatasetName(s.Dataset, taskID)
}

// TaskDatasetName joins a suite dataset prefix and task ID.
func TaskDatasetName(prefix, taskID string) string {
	return prefix + "-" + taskID
}

// Specs returns the task specs directly, for the checks that do not need a
// round trip through LLM Obs, such as verifying mutations still apply.
func (s *Suite) Specs() []*agenteval.TaskSpec {
	out := make([]*agenteval.TaskSpec, 0, len(s.Tasks))
	for i := range s.Tasks {
		out = append(out, &s.Tasks[i].Spec)
	}
	return out
}

var registry = map[string]*Suite{}

// Register adds a suite. It panics on an invalid suite because registration
// happens in init: a broken suite is a programming error that should stop the
// command rather than surface as a confusing result later.
func Register(s *Suite) {
	if err := Validate(s); err != nil {
		panic("suites: " + err.Error())
	}
	if _, dup := registry[s.Name]; dup {
		panic("suites: duplicate suite name " + s.Name)
	}
	registry[s.Name] = s
}

// Validate reports whether a suite is usable. Register calls it, and so does the
// package test, which is what makes a malformed suite fail at `go test` rather
// than when someone tries to run it.
func Validate(s *Suite) error {
	switch {
	case s == nil:
		return fmt.Errorf("suite is nil")
	case s.Name == "":
		return fmt.Errorf("suite has no name")
	case s.Dataset == "":
		return fmt.Errorf("suite %s has no dataset name", s.Name)
	case s.Description == "":
		return fmt.Errorf("suite %s has no description", s.Name)
	case len(s.Tasks) == 0:
		return fmt.Errorf("suite %s has no tasks", s.Name)
	}

	seen := make(map[string]struct{}, len(s.Tasks))
	for i, task := range s.Tasks {
		if _, dup := seen[task.Spec.TaskID]; dup {
			return fmt.Errorf("suite %s: duplicate task_id %q", s.Name, task.Spec.TaskID)
		}
		seen[task.Spec.TaskID] = struct{}{}
		if _, err := task.Records(s.Name); err != nil {
			return fmt.Errorf("suite %s, task %d: %w", s.Name, i, err)
		}
	}
	return nil
}

// Lookup returns a registered suite by name.
func Lookup(name string) (*Suite, error) {
	s, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown suite %q (have: %v)", name, Names())
	}
	return s, nil
}

// All returns every registered suite, ordered by name.
func All() []*Suite {
	out := make([]*Suite, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Names returns every registered suite name, ordered.
func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
