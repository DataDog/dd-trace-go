// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	contribMatrixProgram = "scripts/ci_contrib_matrix.go"
	unitIntegrationFile  = "unit-integration-tests.yml"
	cacheKeyPrefixScript = ".github/actions/setup-go/cache-key-prefix.sh"
	unitContribMatrixJob = "test-contrib-matrix"
	unitContribWorkload  = "unit-contrib"
	unitContribBuildTag  = "standard"
)

var workflowExpression = regexp.MustCompile(`\$\{\{(.*?)\}\}`)

type shardEntry struct {
	Shard   string `json:"shard"`
	Modules string `json:"modules"`
	Count   int    `json:"count"`
}

// runContribMatrix runs the real matrix program from the repository root and
// returns its stdout. selection is written one module per line, as the
// workflow does; nil omits -select.
func runContribMatrix(t *testing.T, root string, shards bool, selection []string) []byte {
	t.Helper()
	args := []string{"run", contribMatrixProgram}
	if shards {
		args = append(args, "-shards")
	}
	if selection != nil {
		file := filepath.Join(t.TempDir(), "select.txt")
		if err := os.WriteFile(file, []byte(strings.Join(selection, "\n")+"\n"), 0o600); err != nil {
			t.Fatalf("write selection: %v", err)
		}
		args = append(args, "-select", file)
	}
	cmd := exec.CommandContext(t.Context(), "go", args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return out
}

func runShardEntries(t *testing.T, root string, selection []string) []shardEntry {
	t.Helper()
	out := runContribMatrix(t, root, true, selection)
	var entries []shardEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		t.Fatalf("-shards output is not an array of shard objects: %v\n%s", err, out)
	}
	return entries
}

// variantTemplate returns the unit-contrib variant text of the matrix job, as
// written in the workflow.
func variantTemplate(t *testing.T, root string) string {
	t.Helper()
	doc, _ := parseWorkflow(t, filepath.Join(root, workflowDir, unitIntegrationFile))
	jobs, _ := doc["jobs"].(map[string]any)
	job, _ := jobs[unitContribMatrixJob].(map[string]any)
	steps, _ := job["steps"].([]any)
	for _, s := range steps {
		step, _ := s.(map[string]any)
		with, _ := step["with"].(map[string]any)
		if with["workload"] == unitContribWorkload {
			variant, _ := with["variant"].(string)
			return variant
		}
	}
	t.Fatalf("%s: no step with workload %s in job %s", unitIntegrationFile, unitContribWorkload, unitContribMatrixJob)
	return ""
}

// renderVariant substitutes the matrix and input expressions the way Actions
// does. Any other expression fails the test, so a variant that starts keying
// on something this test does not model cannot slip through.
func renderVariant(t *testing.T, template string, entry shardEntry) string {
	t.Helper()
	return workflowExpression.ReplaceAllStringFunc(template, func(match string) string {
		inner := strings.TrimSpace(workflowExpression.FindStringSubmatch(match)[1])
		switch {
		case strings.Contains(inner, "matrix.chunk.shard"):
			return strconv.Quote(entry.Shard)
		case strings.Contains(inner, "inputs.build_tags"):
			return strconv.Quote(unitContribBuildTag)
		}
		t.Errorf("variant expression %q is not modelled by this test", inner)
		return match
	})
}

// cachePrefix runs the repository's cache-key-prefix.sh the way the setup-go
// wrapper does.
func cachePrefix(t *testing.T, root, variant string) string {
	t.Helper()
	for _, tool := range []string{"bash", "jq", "openssl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found: %v", tool, err)
		}
	}
	output := filepath.Join(t.TempDir(), "github_output")
	cmd := exec.CommandContext(t.Context(), "bash", filepath.Join(root, cacheKeyPrefixScript))
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"FAMILY=" + unitContribWorkload,
		"VARIANT=" + variant,
		"GITHUB_OUTPUT=" + output,
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cache-key-prefix.sh: %v\n%s", err, out)
	}
	written, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read GITHUB_OUTPUT: %v", err)
	}
	prefix, ok := strings.CutPrefix(strings.TrimSpace(string(written)), "prefix=")
	if !ok {
		t.Fatalf("GITHUB_OUTPUT has no prefix= line: %q", written)
	}
	return prefix
}

// selectionLines turns "./contrib/a/" tokens into the "contrib/a" lines that
// ciselect writes.
func selectionLines(modules []string) []string {
	lines := make([]string, len(modules))
	for i, m := range modules {
		lines[i] = strings.TrimSuffix(strings.TrimPrefix(m, "./"), "/")
	}
	return lines
}

// maxRunnersInProgram reads the runner count from the program source. The
// program is //go:build ignore, so the test cannot import the constant.
func maxRunnersInProgram(t *testing.T, root string) int {
	t.Helper()
	m := regexp.MustCompile(`(?m)^\s*maxRunners\s*=\s*(\d+)\s*$`).
		FindStringSubmatch(readText(t, root, contribMatrixProgram))
	if m == nil {
		t.Fatalf("%s: no maxRunners constant found", contribMatrixProgram)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("maxRunners %q: %v", m[1], err)
	}
	return n
}

// TestNarrowedContribShardsShareTheNightlyCachePrefix is the end-to-end pin for
// the stable shard identity: a pull request that narrows the module selection
// must compute the same cache-key prefix, per shard, as the nightly run that
// tests every module.
func TestNarrowedContribShardsShareTheNightlyCachePrefix(t *testing.T) {
	root, err := repoRoot(".")
	if err != nil {
		t.Fatalf("repoRoot() = %v", err)
	}
	template := variantTemplate(t, root)
	runners := maxRunnersInProgram(t, root)

	all := runShardEntries(t, root, []string{selectAll})
	if len(all) != runners {
		t.Fatalf("ALL run emitted %d entries, want %d", len(all), runners)
	}
	allByShard := map[string]shardEntry{}
	allPrefix := map[string]string{}
	seenPrefix := map[string]string{}
	for k, entry := range all {
		if want := fmt.Sprintf("%d/%d", k, runners); entry.Shard != want {
			t.Errorf("ALL entry %d shard = %q, want %q (denominator must be maxRunners)", k, entry.Shard, want)
		}
		allByShard[entry.Shard] = entry
		prefix := cachePrefix(t, root, renderVariant(t, template, entry))
		if other, dup := seenPrefix[prefix]; dup {
			t.Errorf("shards %s and %s share prefix %s", other, entry.Shard, prefix)
		}
		seenPrefix[prefix] = entry.Shard
		allPrefix[entry.Shard] = prefix
	}

	t.Run("ALL equals the default output", func(t *testing.T) {
		var chunks []string
		if err := json.Unmarshal(runContribMatrix(t, root, false, []string{selectAll}), &chunks); err != nil {
			t.Fatalf("default output is not an array of strings: %v", err)
		}
		if len(chunks) != len(all) {
			t.Fatalf("default output has %d chunks, -shards has %d entries", len(chunks), len(all))
		}
		for k, entry := range all {
			if entry.Modules != chunks[k] {
				t.Errorf("shard %s modules differ from default chunk %d:\n%q\n%q", entry.Shard, k, entry.Modules, chunks[k])
			}
		}
	})

	pick := func(shard string, from, to int) []string {
		return strings.Fields(allByShard[shard].Modules)[from:to]
	}
	selections := []struct {
		name       string
		modules    []string
		wantShards []string
	}{
		{"one module", pick("3/6", 0, 1), []string{"3/6"}},
		{"two modules from one shard", pick("1/6", 0, 2), []string{"1/6"}},
		{
			"modules spanning shards",
			slices.Concat(pick("0/6", 0, 1), pick("2/6", 3, 4), pick("5/6", 0, 2)),
			[]string{"0/6", "2/6", "5/6"},
		},
	}
	for _, tt := range selections {
		t.Run(tt.name, func(t *testing.T) {
			entries := runShardEntries(t, root, selectionLines(tt.modules))
			var gotShards []string
			var gotModules []string
			for _, entry := range entries {
				gotShards = append(gotShards, entry.Shard)
				if strings.TrimSpace(entry.Modules) == "" {
					t.Errorf("shard %s has an empty modules string", entry.Shard)
				}
				fields := strings.Fields(entry.Modules)
				gotModules = append(gotModules, fields...)
				if entry.Count != len(fields) {
					t.Errorf("shard %s count = %d, want %d", entry.Shard, entry.Count, len(fields))
				}
				if got, want := cachePrefix(t, root, renderVariant(t, template, entry)), allPrefix[entry.Shard]; got != want {
					t.Errorf("shard %s narrowed prefix = %s, ALL prefix = %s", entry.Shard, got, want)
				}
				for _, m := range fields {
					if !slices.Contains(strings.Fields(allByShard[entry.Shard].Modules), m) {
						t.Errorf("module %s moved into shard %s", m, entry.Shard)
					}
				}
			}
			if !slices.Equal(gotShards, tt.wantShards) {
				t.Errorf("shards = %v, want %v", gotShards, tt.wantShards)
			}
			slices.Sort(gotModules)
			want := slices.Sorted(slices.Values(tt.modules))
			if !slices.Equal(gotModules, want) {
				t.Errorf("modules = %v, want %v", gotModules, want)
			}
		})
	}

	t.Run("NONE emits an empty array", func(t *testing.T) {
		// The workflow gates on the literal "[]" to skip the matrix job.
		out := strings.TrimSpace(string(runContribMatrix(t, root, true, []string{selectNone})))
		if out != "[]" {
			t.Errorf("-shards with NONE = %q, want []", out)
		}
	})
}

// TestContribMatrixDefaultOutputIsStringArray pins the contract of the smoke
// workflow, which reads each entry as one space-separated module list and does
// not pass -shards.
func TestContribMatrixDefaultOutputIsStringArray(t *testing.T) {
	root, err := repoRoot(".")
	if err != nil {
		t.Fatalf("repoRoot() = %v", err)
	}
	var chunks []string
	out := runContribMatrix(t, root, false, nil)
	if err := json.Unmarshal(out, &chunks); err != nil {
		t.Fatalf("default output is not an array of strings: %v\n%s", err, out)
	}
	if want := maxRunnersInProgram(t, root); len(chunks) != want {
		t.Errorf("default output has %d chunks, want %d", len(chunks), want)
	}
	for i, chunk := range chunks {
		if strings.TrimSpace(chunk) == "" {
			t.Errorf("default chunk %d is empty", i)
		}
	}
}
