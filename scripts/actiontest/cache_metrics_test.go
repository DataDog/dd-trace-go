// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package actiontest

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// payloadScript is the payload builder of .github/actions/cache-metrics,
// relative to this package directory.
const payloadScript = "../../.github/actions/cache-metrics/payload.sh"

type series struct {
	Metric string   `json:"metric"`
	Tags   []string `json:"tags"`
}

func (s series) tag(prefix string) string {
	for _, tag := range s.Tags {
		if value, ok := strings.CutPrefix(tag, prefix); ok {
			return value
		}
	}
	return ""
}

// runPayload runs payload.sh with the action's environment and returns the
// submitted series. Observations are simulated; nothing is sent anywhere.
func runPayload(t *testing.T, observation map[string]any, measureSizes bool) []series {
	t.Helper()
	for _, tool := range []string{"bash", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	metrics, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	sizes := "false"
	if measureSizes {
		sizes = "true"
	}
	script, err := filepath.Abs(payloadScript)
	if err != nil {
		t.Fatal(err)
	}
	// jq prints a null path as the word "null", which names a directory here:
	// without the path filter it would be measured as a real cache.
	workDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(workDir, "null"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"CACHE_METRICS="+string(metrics),
		"MEASURE_SIZES="+sizes,
		"GITHUB_REPOSITORY=DataDog/dd-trace-go",
		"CI_JOB_NAME=test",
		"CI_PIPELINE_NAME=unit-integration-tests",
		"CI_TRIGGER=pull_request",
		"RUNNER_OS=Linux",
		"RUNNER_ARCH=X64",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("payload.sh: %v\n%s", err, stderr.String())
	}
	var payload struct {
		Series []series `json:"series"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v\n%s", err, stdout.String())
	}
	return payload.Series
}

func restore(fields map[string]any) map[string]any {
	entry := map[string]any{"name": "setup_go", "enabled": "true", "outcome": "success"}
	maps.Copy(entry, fields)
	return entry
}

func observation(provider string, restores ...map[string]any) map[string]any {
	if restores == nil {
		restores = []map[string]any{}
	}
	return map[string]any{
		"provider": provider,
		"workload": "unit-core",
		"runtime":  map[string]string{"name": "go", "version": "1.27.1"},
		"restores": restores,
	}
}

func onlyCount(t *testing.T, all []series) series {
	t.Helper()
	var counts []series
	for _, s := range all {
		if s.Metric == "ci.step.cache.restore" {
			counts = append(counts, s)
		}
	}
	if len(counts) != 1 {
		t.Fatalf("got %d restore count series, want 1: %+v", len(counts), all)
	}
	return counts[0]
}

func TestPayloadRestoreClassification(t *testing.T) {
	cases := []struct {
		name         string
		provider     string
		restore      map[string]any
		wantOutcome  string
		wantHitType  string
		wantNoSeries bool
	}{
		{"cloudx without matched key, cache_hit true", "cloudx",
			map[string]any{"cache_hit": "true"}, "hit", "exact", false},
		{"cloudx without matched key, cache_hit false", "cloudx",
			map[string]any{"cache_hit": "false"}, "miss", "miss", false},
		{"github-cache without matched key, cache_hit true", "github-cache",
			map[string]any{"cache_hit": "true"}, "hit", "unknown", false},
		{"github-cache without matched key, cache_hit false", "github-cache",
			map[string]any{"cache_hit": "false"}, "miss", "miss", false},
		{"cloudx tools entry, exact hit", "cloudx",
			map[string]any{"cache_hit": "true", "cache_matched_key": "k"}, "hit", "exact", false},
		{"cloudx tools entry, prefix restore", "cloudx",
			map[string]any{"cache_hit": "false", "cache_matched_key": "k-old"}, "hit", "partial", false},
		{"cloudx tools entry, cold miss", "cloudx",
			map[string]any{"cache_hit": "false", "cache_matched_key": ""}, "miss", "miss", false},
		{"cloudx restored true is a prefix restore", "cloudx",
			map[string]any{"cache_hit": "false", "restored": "true"}, "hit", "partial", false},
		{"cloudx restored false is a cold miss", "cloudx",
			map[string]any{"cache_hit": "false", "restored": "false"}, "miss", "miss", false},
		{"cloudx exact hit wins over restored", "cloudx",
			map[string]any{"cache_hit": "true", "restored": "true"}, "hit", "exact", false},
		{"disabled restore emits nothing", "cloudx",
			map[string]any{"enabled": "false", "cache_hit": "true"}, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			all := runPayload(t, observation(tc.provider, restore(tc.restore)), false)
			if tc.wantNoSeries {
				if len(all) != 0 {
					t.Fatalf("got series %+v, want none", all)
				}
				return
			}
			got := onlyCount(t, all)
			if outcome, hitType := got.tag("outcome:"), got.tag("hit_type:"); outcome != tc.wantOutcome || hitType != tc.wantHitType {
				t.Fatalf("outcome/hit_type = %s/%s, want %s/%s", outcome, hitType, tc.wantOutcome, tc.wantHitType)
			}
			if got.tag("provider:") != tc.provider || got.tag("cache_name:") != "setup_go" {
				t.Fatalf("tags = %v", got.Tags)
			}
		})
	}
}

func TestPayloadSizeSeries(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), make([]byte, 8192), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func(paths ...map[string]any) map[string]any {
		obs := observation("github-cache")
		obs["cache_paths"] = paths
		return obs
	}
	sizeSeries := func(all []series) []series {
		var out []series
		for _, s := range all {
			if s.Metric == "ci.cache.disk_size_bytes" {
				out = append(out, s)
			}
		}
		return out
	}

	t.Run("measures an existing directory", func(t *testing.T) {
		got := sizeSeries(runPayload(t, build(map[string]any{"name": "go_mod", "path": dir}), true))
		if len(got) != 1 || got[0].tag("cache_name:") != "go_mod" || !slices.Contains(got[0].Tags, "phase:end_of_job") {
			t.Fatalf("size series = %+v", got)
		}
	})
	t.Run("null path yields no size series", func(t *testing.T) {
		got := sizeSeries(runPayload(t, build(map[string]any{"name": "go_mod", "path": nil}), true))
		if len(got) != 0 {
			t.Fatalf("size series = %+v, want none", got)
		}
	})
	t.Run("empty path yields no size series", func(t *testing.T) {
		got := sizeSeries(runPayload(t, build(map[string]any{"name": "go_mod", "path": ""}), true))
		if len(got) != 0 {
			t.Fatalf("size series = %+v, want none", got)
		}
	})
	t.Run("sizes are skipped unless requested", func(t *testing.T) {
		got := sizeSeries(runPayload(t, build(map[string]any{"name": "go_mod", "path": dir}), false))
		if len(got) != 0 {
			t.Fatalf("size series = %+v, want none", got)
		}
	})
}
