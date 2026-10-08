// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"path"
	"sort"
	"strings"
)

// result is the verdict for one change set.
type result struct {
	// Gates is every gate name, mapped to whether its work must run.
	Gates map[string]bool `json:"gates"`
	// RunAll records that the change set could not be bounded, so everything
	// runs. Reasons explains why, for the job summary.
	RunAll  bool     `json:"run_all"`
	Reasons []string `json:"reasons"`
	// Explain maps each changed path to the component that claimed it.
	Explain map[string]string `json:"explain"`
	// SeedPaths are paths claimed by a module-scoped component; the contrib
	// matrix resolves them to modules. Empty whenever RunAll is set.
	SeedPaths []string `json:"seed_paths"`
	// DependentModules is the union of the measured reverse dependencies of
	// every leaf component touched.
	DependentModules []string `json:"dependent_modules"`
}

func (t *table) runAll(reasons ...string) *result {
	r := &result{
		Gates:   make(map[string]bool, len(t.Gates)),
		RunAll:  true,
		Reasons: reasons,
		Explain: map[string]string{},
	}
	for _, g := range t.Gates {
		r.Gates[g] = true
	}
	return r
}

// classify maps a change set onto gates.
//
// The invariant the whole design rests on: anything we cannot account for
// enables every gate. An empty change set, an unclassified path, and an
// unrecognised workflow file are all "cannot account for".
func (t *table) classify(changed []string, g *workflowGraph) (*result, error) {
	if len(changed) == 0 {
		return t.runAll("no changed files were reported"), nil
	}

	r := &result{
		Gates:   make(map[string]bool, len(t.Gates)),
		Explain: make(map[string]string, len(changed)),
	}
	for _, gate := range t.Gates {
		r.Gates[gate] = false
	}

	var (
		seeds     []string
		depMods   = map[string]bool{}
		reasons   []string
		everyGate bool
	)

	for _, file := range changed {
		c := t.componentFor(file)
		if c == nil {
			everyGate = true
			reasons = append(reasons, file+": matches no component in "+tableRelPath)
			r.Explain[file] = "(unclassified)"
			continue
		}
		r.Explain[file] = c.ID

		gates := c.Gates
		if c.SelfReference {
			resolved, known, err := t.gatesForWorkflowFile(g, path.Base(file))
			if err != nil {
				return nil, err
			}
			if !known {
				everyGate = true
				reasons = append(reasons, file+": workflow not listed in workflow-gates")
				continue
			}
			// A workflow edit always needs actionlint, on top of whatever the
			// workflow itself gates.
			gates = append(append([]string(nil), c.Gates...), resolved...)
		}

		expanded, err := t.expand(gates, nil)
		if err != nil {
			return nil, err
		}
		for _, gate := range expanded {
			r.Gates[gate] = true
		}

		if c.ModuleScoped {
			seeds = append(seeds, file)
		}
		for _, m := range c.DependentModules {
			depMods[m] = true
		}
	}

	if everyGate {
		out := t.runAll(reasons...)
		out.Explain = r.Explain
		return out, nil
	}

	// A component that enables every gate is indistinguishable from run-all as
	// far as downstream consumers are concerned; say so explicitly so the
	// contrib matrix does not try to narrow.
	all := true
	for _, gate := range t.Gates {
		if !r.Gates[gate] {
			all = false
			break
		}
	}
	if all {
		r.RunAll = true
		r.Reasons = append(r.Reasons, "a changed path belongs to a component that requires every gate")
	} else {
		r.SeedPaths = seeds
		for m := range depMods {
			r.DependentModules = append(r.DependentModules, m)
		}
		sort.Strings(r.DependentModules)
	}
	return r, nil
}

// anyGateWithPrefix reports whether any enabled gate starts with prefix.
func (r *result) anyGateWithPrefix(prefix string) bool {
	for gate, on := range r.Gates {
		if on && strings.HasPrefix(gate, prefix) {
			return true
		}
	}
	return false
}
