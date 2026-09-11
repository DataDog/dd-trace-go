// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// tableRelPath is where the rule table lives, relative to the repository root.
const tableRelPath = ".github/ci-components.yml"

// component is one row of the table. Ordering is significant: the first
// component with a matching pattern claims the path.
type component struct {
	ID               string   `yaml:"id"`
	Why              string   `yaml:"why"`
	Paths            []string `yaml:"paths"`
	Gates            []string `yaml:"gates"`
	DependentModules []string `yaml:"dependent-modules"`
	ModuleScoped     bool     `yaml:"module-scoped"`
	SelfReference    bool     `yaml:"self-reference"`

	patterns []pattern
}

type table struct {
	Version           int                 `yaml:"version"`
	Gates             []string            `yaml:"gates"`
	Groups            map[string][]string `yaml:"groups"`
	WorkflowGates     map[string][]string `yaml:"workflow-gates"`
	NativePathFilters []string            `yaml:"native-path-filters"`
	Components        []component         `yaml:"components"`

	gateSet map[string]bool
}

// repoRoot walks up from dir until it finds the rule table. go:embed cannot
// reach outside the package directory, and duplicating the table under
// scripts/ would defeat the point of having one reviewable copy.
func repoRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, tableRelPath)); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no %s found in %s or any parent", tableRelPath, dir)
		}
		abs = parent
	}
}

func loadTable(root string) (*table, error) {
	raw, err := os.ReadFile(filepath.Join(root, tableRelPath))
	if err != nil {
		return nil, err
	}
	var t table
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&t); err != nil {
		return nil, fmt.Errorf("parse %s: %w", tableRelPath, err)
	}
	if err := t.compile(); err != nil {
		return nil, fmt.Errorf("%s: %w", tableRelPath, err)
	}
	return &t, nil
}

func (t *table) compile() error {
	if t.Version != 1 {
		return fmt.Errorf("unsupported version %d", t.Version)
	}
	if len(t.Gates) == 0 {
		return errors.New("no gates declared")
	}
	t.gateSet = make(map[string]bool, len(t.Gates))
	for _, g := range t.Gates {
		if t.gateSet[g] {
			return fmt.Errorf("duplicate gate %q", g)
		}
		t.gateSet[g] = true
	}

	for name, members := range t.Groups {
		if _, err := t.expand(members, nil); err != nil {
			return fmt.Errorf("group %q: %w", name, err)
		}
	}
	for wf, gates := range t.WorkflowGates {
		if _, err := t.expand(gates, nil); err != nil {
			return fmt.Errorf("workflow-gates[%s]: %w", wf, err)
		}
	}

	seen := make(map[string]bool, len(t.Components))
	for i := range t.Components {
		c := &t.Components[i]
		if c.ID == "" {
			return fmt.Errorf("component %d has no id", i)
		}
		if seen[c.ID] {
			return fmt.Errorf("duplicate component id %q", c.ID)
		}
		seen[c.ID] = true
		if len(c.Paths) == 0 {
			return fmt.Errorf("component %q has no paths", c.ID)
		}
		if _, err := t.expand(c.Gates, nil); err != nil {
			return fmt.Errorf("component %q: %w", c.ID, err)
		}
		c.patterns = make([]pattern, 0, len(c.Paths))
		for _, raw := range c.Paths {
			p, err := parsePattern(raw)
			if err != nil {
				return fmt.Errorf("component %q: %w", c.ID, err)
			}
			c.patterns = append(c.patterns, p)
		}
	}
	return nil
}

// expand resolves "@group" references into concrete gate names.
func (t *table) expand(names []string, seen map[string]bool) ([]string, error) {
	if seen == nil {
		seen = map[string]bool{}
	}
	var out []string
	for _, n := range names {
		if !strings.HasPrefix(n, "@") {
			if !t.gateSet[n] {
				return nil, fmt.Errorf("unknown gate %q", n)
			}
			out = append(out, n)
			continue
		}
		group := strings.TrimPrefix(n, "@")
		if seen[group] {
			return nil, fmt.Errorf("group cycle at %q", group)
		}
		members, ok := t.Groups[group]
		if !ok {
			return nil, fmt.Errorf("unknown group %q", n)
		}
		seen[group] = true
		nested, err := t.expand(members, seen)
		delete(seen, group)
		if err != nil {
			return nil, err
		}
		out = append(out, nested...)
	}
	return out, nil
}

// componentFor returns the first component claiming file, or nil.
func (t *table) componentFor(file string) *component {
	for i := range t.Components {
		for _, p := range t.Components[i].patterns {
			if p.match(file) {
				return &t.Components[i]
			}
		}
	}
	return nil
}

func (t *table) allGates() []string {
	out := append([]string(nil), t.Gates...)
	sort.Strings(out)
	return out
}
