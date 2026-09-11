// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

const workflowDir = ".github/workflows"

// localCall matches a reusable-workflow invocation of a sibling workflow, e.g.
//
//	uses: ./.github/workflows/unit-integration-tests.yml
var localCall = regexp.MustCompile(`uses:\s*\./\.github/workflows/([A-Za-z0-9._-]+\.ya?ml)`)

// workflowGraph records, for each workflow file, the workflows that invoke it
// through `workflow_call`. Editing a reusable workflow has to enable the gates
// of everything that calls it, and hand-maintaining that list would rot.
type workflowGraph struct {
	files   []string            // base names present on disk
	callers map[string][]string // callee base name -> direct caller base names
}

func loadWorkflowGraph(root string) (*workflowGraph, error) {
	entries, err := os.ReadDir(filepath.Join(root, workflowDir))
	if err != nil {
		return nil, err
	}
	g := &workflowGraph{callers: map[string][]string{}}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if ext := filepath.Ext(name); ext != ".yml" && ext != ".yaml" {
			continue
		}
		g.files = append(g.files, name)

		body, err := os.ReadFile(filepath.Join(root, workflowDir, name))
		if err != nil {
			return nil, err
		}
		for _, m := range localCall.FindAllStringSubmatch(string(body), -1) {
			callee := m[1]
			if callee == name {
				continue
			}
			g.callers[callee] = append(g.callers[callee], name)
		}
	}
	sort.Strings(g.files)
	for k := range g.callers {
		sort.Strings(g.callers[k])
	}
	return g, nil
}

// closure returns name plus every workflow that transitively calls it.
func (g *workflowGraph) closure(name string) []string {
	seen := map[string]bool{name: true}
	queue := []string{name}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, caller := range g.callers[cur] {
			if !seen[caller] {
				seen[caller] = true
				queue = append(queue, caller)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// gatesForWorkflowFile resolves the gates a workflow edit requires: the gates
// the workflow owns, plus those of every workflow that calls it. An unknown
// file name is reported as such so the caller can escalate to "run everything"
// rather than guess.
func (t *table) gatesForWorkflowFile(g *workflowGraph, base string) (gates []string, known bool, err error) {
	if _, ok := t.WorkflowGates[base]; !ok {
		return nil, false, nil
	}
	for _, wf := range g.closure(base) {
		owned, ok := t.WorkflowGates[wf]
		if !ok {
			// A caller we have no entry for. Refuse to guess.
			return nil, false, nil
		}
		expanded, err := t.expand(owned, nil)
		if err != nil {
			return nil, false, fmt.Errorf("workflow %s: %w", wf, err)
		}
		gates = append(gates, expanded...)
	}
	return gates, true, nil
}
