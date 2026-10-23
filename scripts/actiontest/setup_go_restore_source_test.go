// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package actiontest

import (
	"maps"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// depHashFixture is a `hashFiles('**/go.*')`-shaped digest (64 hex chars),
// the same value embedded in the fixture keys below.
const depHashFixture = "60e668c987fcc72f27a63db16f95cea76a22700c35ae41b32482e8e04c9f6380"

// matchedKeyFixture builds a cloudx save key in the layout verified at the
// pinned provider revision:
// go-cache-<os>-<arch>-ddtg-cx1-<family>-<digest16>-<go>-<branch>-<dep-hash>-<run_id>.
func matchedKeyFixture(branch string) string {
	return "go-cache-Windows-X64-ddtg-cx1-multi-os-314198ec86eada43-1.27.2-" +
		branch + "-" + depHashFixture + "-38402253744"
}

// deriveRestoreSource runs the setup-go "Derive restore source" step with the
// given environment and returns its GITHUB_OUTPUT fields.
func deriveRestoreSource(t *testing.T, env map[string]string) map[string]string {
	t.Helper()
	base := map[string]string{
		"CACHE_HIT":      "false",
		"MATCHED_KEY":    "",
		"DEP_HASH":       depHashFixture,
		"DEFAULT_BRANCH": "main",
		"CURRENT_BRANCH": "feature",
	}
	maps.Copy(base, env)
	written, out, err := runSetupGoStep(t, setupGoStep(t, "Derive restore source"), base)
	if err != nil {
		t.Fatalf("derive step failed: %v\n%s", err, out)
	}
	fields := map[string]string{}
	for line := range strings.SplitSeq(strings.TrimSpace(written), "\n") {
		key, value, _ := strings.Cut(line, "=")
		fields[key] = value
	}
	return fields
}

func TestSetupGoDeriveRestoreSource(t *testing.T) {
	cases := []struct {
		name         string
		env          map[string]string
		wantSource   string
		wantDepMatch string
	}{
		{
			name:         "cold miss",
			env:          map[string]string{"MATCHED_KEY": ""},
			wantSource:   "none",
			wantDepMatch: "false",
		},
		{
			name:         "exact hit",
			env:          map[string]string{"CACHE_HIT": "true", "MATCHED_KEY": matchedKeyFixture("main")},
			wantSource:   "exact",
			wantDepMatch: "true",
		},
		{
			name:         "default-branch fallback restore",
			env:          map[string]string{"MATCHED_KEY": matchedKeyFixture("main")},
			wantSource:   "default",
			wantDepMatch: "true",
		},
		{
			name:         "same-branch restore",
			env:          map[string]string{"MATCHED_KEY": matchedKeyFixture("eliottness/appsec-disable-features")},
			wantSource:   "branch",
			wantDepMatch: "true",
		},
		{
			name: "dependency hash differs",
			env: map[string]string{
				"DEP_HASH":    "0000000000000000000000000000000000000000000000000000000000000000",
				"MATCHED_KEY": matchedKeyFixture("main"),
			},
			wantSource:   "default",
			wantDepMatch: "false",
		},
		{
			name:         "non-default repository branch",
			env:          map[string]string{"DEFAULT_BRANCH": "develop", "MATCHED_KEY": matchedKeyFixture("develop")},
			wantSource:   "default",
			wantDepMatch: "true",
		},
		{
			name:         "unparseable matched key stays a branch restore",
			env:          map[string]string{"MATCHED_KEY": "go-cache-Linux-X64-legacy-layout"},
			wantSource:   "branch",
			wantDepMatch: "false",
		},
		{
			// A nightly run on main restoring its own earlier entry is a
			// same-branch restore, not a fallback.
			name:         "same-branch restore on the default branch",
			env:          map[string]string{"MATCHED_KEY": matchedKeyFixture("main"), "CURRENT_BRANCH": "main"},
			wantSource:   "branch",
			wantDepMatch: "true",
		},
		{
			// Go prerelease versions carry one hyphen cluster in the key;
			// the branch must still parse after it.
			name: "prerelease Go version",
			env: map[string]string{
				"MATCHED_KEY": "go-cache-macOS-ARM64-ddtg-cx1-multi-os-314198ec86eada43-1.28.0-rc.1-" +
					"main-" + depHashFixture + "-38402253744",
			},
			wantSource:   "default",
			wantDepMatch: "true",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fields := deriveRestoreSource(t, tc.env)
			if fields["restore-source"] != tc.wantSource {
				t.Errorf("restore-source = %q, want %q", fields["restore-source"], tc.wantSource)
			}
			if fields["dep-match"] != tc.wantDepMatch {
				t.Errorf("dep-match = %q, want %q", fields["dep-match"], tc.wantDepMatch)
			}
			if _, ok := fields["cache-matched-key"]; !ok {
				t.Fatal("derive step wrote no cache-matched-key output")
			}
		})
	}
}

// TestSetupGoDeriveRestoreSourceStepGatedOnCloudx pins the derive step's
// condition to the cloudx provider and an enabled cache, so restore-source
// telemetry cannot appear for github-cache jobs.
func TestSetupGoDeriveRestoreSourceStepGatedOnCloudx(t *testing.T) {
	cond := setupGoStep(t, "Derive restore source").If
	if !strings.Contains(cond, "inputs.cache-provider == 'cloudx'") ||
		!strings.Contains(cond, "inputs.cache") {
		t.Errorf("derive step is not gated on the cloudx provider and cache: if: %q", cond)
	}
}

func TestSetupGoObservationRestoreSourceFields(t *testing.T) {
	record := observationRecord(t, map[string]string{
		"PROVIDER":          "cloudx",
		"WORKLOAD":          "multi-os",
		"REQUESTED_VERSION": "1.27",
		"RESOLVED_VERSION":  "1.27.2",
		"CACHE_ENABLED":     "true",
		"SETUP_OUTCOME":     "success",
		"SETUP_CACHE_HIT":   "false",
		"RESTORED":          "true",
		"MATCHED_KEY":       matchedKeyFixture("main"),
		"RESTORE_SOURCE":    "default",
		"DEP_MATCH":         "true",
		"SEED_KEY":          "true",
		"SAVE_RESOLVED":     "false",
	})
	entry := setupGoRestore(t, record)
	for key, want := range map[string]string{
		"cache_matched_key": matchedKeyFixture("main"),
		"restore_source":    "default",
		"dep_match":         "true",
		"restored":          "true",
	} {
		if got := entry[key]; got != want {
			t.Errorf("setup_go entry %s = %v, want %q", key, got, want)
		}
	}
}

// TestSetupGoObservationOmitsRestoreSourceWithoutProvider: when the cloudx
// provider did not complete, the derive step never ran and the record must
// not carry half of the restore-source evidence.
func TestSetupGoObservationOmitsRestoreSourceWithoutProvider(t *testing.T) {
	record := observationRecord(t, map[string]string{
		"PROVIDER":          "github-cache",
		"WORKLOAD":          "static-copyright",
		"REQUESTED_VERSION": "stable",
		"RESOLVED_VERSION":  "1.27.2",
		"CACHE_ENABLED":     "true",
		"SETUP_OUTCOME":     "success",
		"SETUP_CACHE_HIT":   "true",
		"RESTORED":          "",
		"MATCHED_KEY":       "",
		"RESTORE_SOURCE":    "",
		"DEP_MATCH":         "",
		"SEED_KEY":          "",
		"SAVE_RESOLVED":     "",
	})
	entry := setupGoRestore(t, record)
	for _, key := range []string{"cache_matched_key", "restore_source", "dep_match", "restored"} {
		if _, present := entry[key]; present {
			t.Errorf("github-cache entry carries %s: %v", key, entry[key])
		}
	}
}

// TestSetupGoExposesRestoreSourceOutputs pins the action-level outputs that
// surface the matched key and the restore source to callers.
func TestSetupGoExposesRestoreSourceOutputs(t *testing.T) {
	raw, err := os.ReadFile(setupGoAction)
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Outputs map[string]struct {
			Value string `yaml:"value"`
		} `yaml:"outputs"`
	}
	if err := yaml.Unmarshal(raw, &action); err != nil {
		t.Fatal(err)
	}
	for name, wantStep := range map[string]string{
		"cache-matched-key": "${{ steps.restore-source.outputs.cache-matched-key }}",
		"restore-source":    "${{ steps.restore-source.outputs.restore-source }}",
	} {
		out, ok := action.Outputs[name]
		if !ok {
			t.Errorf("setup-go action lacks the %s output", name)
			continue
		}
		if out.Value != wantStep {
			t.Errorf("output %s = %q, want %q", name, out.Value, wantStep)
		}
	}
}

// ---- save policy (seed input resolution) ----

// resolveSavePolicy runs the setup-go "Resolve save policy" step with the
// given environment and returns the save output.
func resolveSavePolicy(t *testing.T, env map[string]string) string {
	t.Helper()
	base := map[string]string{
		"SEED":           "false",
		"EVENT":          "pull_request",
		"REF":            "refs/pull/5570/merge",
		"DEFAULT_BRANCH": "main",
	}
	maps.Copy(base, env)
	written, out, err := runSetupGoStep(t, setupGoStep(t, "Resolve save policy"), base)
	if err != nil {
		t.Fatalf("save-policy step failed: %v\n%s", err, out)
	}
	value, ok := strings.CutPrefix(strings.TrimSpace(written), "save=")
	if !ok {
		t.Fatalf("GITHUB_OUTPUT has no save= line: %q", written)
	}
	return value
}

// TestSetupGoSavePolicyTruthTable covers every event x ref x seed
// combination that decides whether a run may save a seed entry.
func TestSetupGoSavePolicyTruthTable(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "nightly schedule with a seed key saves",
			env:  map[string]string{"SEED": "true", "EVENT": "schedule", "REF": "refs/heads/main"},
			want: "true",
		},
		{
			name: "nightly schedule without a seed key restores only",
			env:  map[string]string{"SEED": "false", "EVENT": "schedule", "REF": "refs/heads/main"},
			want: "false",
		},
		{
			name: "manual dispatch on the default branch with a seed key saves",
			env:  map[string]string{"SEED": "true", "EVENT": "workflow_dispatch", "REF": "refs/heads/main"},
			want: "true",
		},
		{
			name: "manual dispatch on a feature branch never saves",
			env:  map[string]string{"SEED": "true", "EVENT": "workflow_dispatch", "REF": "refs/heads/feature"},
			want: "false",
		},
		{
			name: "manual dispatch against a non-default main-named branch",
			env: map[string]string{"SEED": "true", "EVENT": "workflow_dispatch",
				"REF": "refs/heads/main", "DEFAULT_BRANCH": "develop"},
			want: "false",
		},
		{
			name: "a pull request with a seed key restores only",
			env:  map[string]string{"SEED": "true", "EVENT": "pull_request", "REF": "refs/pull/5570/merge"},
			want: "false",
		},
		{
			name: "a merge-queue push with a seed key restores only",
			env: map[string]string{"SEED": "true", "EVENT": "push",
				"REF": "refs/heads/mq-working-branch-x"},
			want: "false",
		},
		{
			name: "a release push with a seed key restores only",
			env:  map[string]string{"SEED": "true", "EVENT": "push", "REF": "refs/tags/v2.11.0"},
			want: "false",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveSavePolicy(t, tc.env); got != tc.want {
				t.Errorf("save = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSetupGoObservationSeedFields: the observation carries the seed-key
// membership and the resolved save mode for the payload's tags.
func TestSetupGoObservationSeedFields(t *testing.T) {
	record := observationRecord(t, map[string]string{
		"PROVIDER":          "cloudx",
		"WORKLOAD":          "multi-os",
		"REQUESTED_VERSION": "1.27",
		"RESOLVED_VERSION":  "1.27.2",
		"CACHE_ENABLED":     "true",
		"SETUP_OUTCOME":     "success",
		"SETUP_CACHE_HIT":   "false",
		"RESTORED":          "false",
		"MATCHED_KEY":       "",
		"RESTORE_SOURCE":    "none",
		"DEP_MATCH":         "false",
		"SEED_KEY":          "true",
		"SAVE_RESOLVED":     "false",
	})
	entry := setupGoRestore(t, record)
	if entry["seed_key"] != "true" || entry["save"] != "false" {
		t.Errorf("seed_key/save = %v/%v, want true/false (PR run of a seed-set key)", entry["seed_key"], entry["save"])
	}
}

// TestSetupGoExposesSaveOutput pins the action-level save output wiring.
func TestSetupGoExposesSaveOutput(t *testing.T) {
	raw, err := os.ReadFile(setupGoAction)
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Outputs map[string]struct {
			Value string `yaml:"value"`
		} `yaml:"outputs"`
	}
	if err := yaml.Unmarshal(raw, &action); err != nil {
		t.Fatal(err)
	}
	out, ok := action.Outputs["save"]
	if !ok {
		t.Fatal("setup-go action lacks the save output")
	}
	if out.Value != "${{ steps.save-policy.outputs.save }}" {
		t.Errorf("save output = %q", out.Value)
	}
}

// TestSetupGoSavePolicyRunsBeforeProvider: the cloudx step consumes the
// save output, so the policy must resolve first or a seed run passes an
// empty save and never writes its entry.
func TestSetupGoSavePolicyRunsBeforeProvider(t *testing.T) {
	raw, err := os.ReadFile(setupGoAction)
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Runs struct {
			Steps []struct {
				Name string `yaml:"name"`
			} `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(raw, &action); err != nil {
		t.Fatal(err)
	}
	policy, provider := -1, -1
	for i, s := range action.Runs.Steps {
		switch s.Name {
		case "Resolve save policy":
			policy = i
		case "Isolate and refresh Go build caches":
			provider = i
		}
	}
	if policy == -1 || provider == -1 {
		t.Fatalf("steps not found: policy=%d provider=%d", policy, provider)
	}
	if policy > provider {
		t.Errorf("Resolve save policy (step %d) runs after the provider (step %d)", policy, provider)
	}
}
