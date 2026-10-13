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
