// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "testing"

func baseResolutionInput(command, requested, source string) VersionResolutionInput {
	return VersionResolutionInput{
		RequestKey:       "123:789",
		RequestSHA256:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Command:          command,
		RequestedVersion: requested,
		ReleaseLine:      "v2.11",
		SourceVersion:    source,
		RemoteRefs:       RemoteRefs{Complete: true, Branches: map[string]string{}, Tags: map[string]string{}},
	}
}

func TestResolvePrepareDevelopmentToReleaseAndNextDevelopment(t *testing.T) {
	input := baseResolutionInput("release:prepare", "auto", "v2.11.0-dev")
	resolved, err := ResolveVersion(input)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ResolvedVersion != "v2.11.0" || resolved.DevelopmentVersion != "v2.12.0-dev" || resolved.ReleaseBranch != "release-v2.11.x" || resolved.DevelopmentBranch != "dev-v2.12.x" {
		t.Fatalf("unexpected resolution: %#v", resolved)
	}
}

func TestResolvePrepareRejectsNonzeroPatchAndMismatchedLine(t *testing.T) {
	cases := []struct {
		name      string
		requested string
		source    string
		code      string
	}{
		{name: "nonzero patch", requested: "v2.11.1", source: "v2.11.0-dev", code: "prepare_patch_not_zero"},
		{name: "mismatched issue line", requested: "v2.12.0", source: "v2.11.0-dev", code: "line_mismatch"},
		{name: "wrong source line", requested: "auto", source: "v2.12.0-dev", code: "source_line_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveVersion(baseResolutionInput("release:prepare", tc.requested, tc.source))
			if ErrorCode(err) != tc.code {
				t.Fatalf("error = %q, want %q", ErrorCode(err), tc.code)
			}
		})
	}
}

func TestResolvePrepareRejectsUnattributedExistingBranches(t *testing.T) {
	input := baseResolutionInput("release:prepare", "auto", "v2.11.0-dev")
	input.RemoteRefs.Branches["refs/heads/release-v2.11.x"] = "abc"
	_, err := ResolveVersion(input)
	if ErrorCode(err) != "branch_exists_without_record" || ClassOf(err) != ErrorClassStateConflict {
		t.Fatalf("error = %q/%q, want state_conflict branch_exists_without_record", ClassOf(err), ErrorCode(err))
	}
}

func TestResolvePromotionFromDevelopmentToRC(t *testing.T) {
	resolved, err := ResolveVersion(baseResolutionInput("release:promote", "auto", "v2.11.0-dev"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ResolvedVersion != "v2.11.0-rc.1" {
		t.Fatalf("resolved version = %q, want v2.11.0-rc.1", resolved.ResolvedVersion)
	}
}

func TestResolvePromotionAdvancesRCUsingNumericSuffixOrdering(t *testing.T) {
	input := baseResolutionInput("release:promote", "auto", "v2.11.0-rc.9")
	input.RemoteRefs.Tags = map[string]string{
		"v2.11.0-rc.9":  "sha",
		"v2.11.0-rc.10": "sha",
	}
	resolved, err := ResolveVersion(input)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ResolvedVersion != "v2.11.0-rc.11" {
		t.Fatalf("resolved version = %q, want v2.11.0-rc.11", resolved.ResolvedVersion)
	}
}

func TestResolvePromotionExplicitNewPatchStartsCandidateCycle(t *testing.T) {
	resolved, err := ResolveVersion(baseResolutionInput("release:promote", "v2.11.1", "v2.11.0"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ResolvedVersion != "v2.11.1-rc.1" {
		t.Fatalf("resolved version = %q, want v2.11.1-rc.1", resolved.ResolvedVersion)
	}
}

func TestResolveRejectsStableAutoAndExistingGA(t *testing.T) {
	cases := []struct {
		name  string
		input VersionResolutionInput
		code  string
		class ErrorClass
	}{
		{name: "promote stable auto", input: baseResolutionInput("release:promote", "auto", "v2.11.0"), code: "stable_auto_rejected", class: ErrorClassRequestRejected},
		{name: "release stable auto", input: baseResolutionInput("release:release", "auto", "v2.11.0"), code: "source_line_mismatch", class: ErrorClassRequestRejected},
		{name: "existing GA", input: func() VersionResolutionInput {
			input := baseResolutionInput("release:release", "auto", "v2.11.0-rc.2")
			input.RemoteRefs.Tags["v2.11.0"] = "sha"
			return input
		}(), code: "existing_ga", class: ErrorClassStateConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveVersion(tc.input)
			if ErrorCode(err) != tc.code || ClassOf(err) != tc.class {
				t.Fatalf("error = %q/%q, want %q/%q", ClassOf(err), ErrorCode(err), tc.class, tc.code)
			}
		})
	}
}

func TestResolveReleaseFromRCToGA(t *testing.T) {
	resolved, err := ResolveVersion(baseResolutionInput("release:release", "auto", "v2.11.0-rc.3"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ResolvedVersion != "v2.11.0" {
		t.Fatalf("resolved version = %q, want v2.11.0", resolved.ResolvedVersion)
	}
}

func TestResolveReleaseRejectsGABelowHighestRootGA(t *testing.T) {
	input := baseResolutionInput("release:release", "auto", "v2.11.0-rc.3")
	input.RemoteRefs.Tags = map[string]string{
		"v2.12.0":          "sha",
		"moduleA/v9.99.99": "ignored module tag",
	}
	_, err := ResolveVersion(input)
	if ErrorCode(err) != "ga_below_highest" || ClassOf(err) != ErrorClassRequestRejected {
		t.Fatalf("error = %q/%q, want request_rejected ga_below_highest", ClassOf(err), ErrorCode(err))
	}
}

func TestResolveDoesNotTreatIncompleteRemoteRefsAsEmpty(t *testing.T) {
	input := baseResolutionInput("release:promote", "auto", "v2.11.0-dev")
	input.RemoteRefs.Complete = false
	_, err := ResolveVersion(input)
	if ErrorCode(err) != "remote_refs_incomplete" || ClassOf(err) != ErrorClassEvidenceIncomplete {
		t.Fatalf("error = %q/%q, want evidence_incomplete remote_refs_incomplete", ClassOf(err), ErrorCode(err))
	}
}

func TestResolveBlocksIncompleteTagsAndIncompletePriorOperations(t *testing.T) {
	cases := []struct {
		name  string
		input VersionResolutionInput
		code  string
	}{
		{name: "partial tags", input: func() VersionResolutionInput {
			input := baseResolutionInput("release:promote", "auto", "v2.11.0-rc.1")
			input.RemoteRefs.IncompleteTagVersions = []string{"v2.11.0"}
			return input
		}(), code: "incomplete_tags"},
		{name: "prior operation", input: func() VersionResolutionInput {
			input := baseResolutionInput("release:promote", "auto", "v2.11.0-dev")
			input.ExistingOperations = []ExistingOperation{{RequestKey: "123:999", RequestSHA256: input.RequestSHA256, Command: "release:promote", ReleaseLine: "v2.11", ResolvedVersion: "v2.11.0-rc.1", Phase: "reserved"}}
			return input
		}(), code: "incomplete_operation"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveVersion(tc.input)
			if ErrorCode(err) != tc.code || ClassOf(err) != ErrorClassStateConflict {
				t.Fatalf("error = %q/%q, want state_conflict %q", ClassOf(err), ErrorCode(err), tc.code)
			}
		})
	}
}

func TestResolveSameRequestResumeBeforeNextVersionRules(t *testing.T) {
	input := baseResolutionInput("release:promote", "auto", "v2.11.0-rc.1")
	input.RemoteRefs.Tags = map[string]string{"v2.11.0-rc.2": "sha"}
	input.ExistingOperations = []ExistingOperation{{RequestKey: input.RequestKey, RequestSHA256: input.RequestSHA256, Command: input.Command, ReleaseLine: input.ReleaseLine, ResolvedVersion: "v2.11.0-rc.2", Phase: "reserved"}}
	resolved, err := ResolveVersion(input)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Resume || resolved.ResolvedVersion != "v2.11.0-rc.2" {
		t.Fatalf("resume resolution = %#v, want recorded v2.11.0-rc.2", resolved)
	}
}

func TestDevelopmentVersionSkipsExistingBareDevTag(t *testing.T) {
	input := baseResolutionInput("release:prepare", "auto", "v2.11.0-dev")
	input.RemoteRefs.Tags = map[string]string{
		"v2.12.0-dev":   "sha",
		"v2.12.0-dev.2": "sha",
	}
	resolved, err := ResolveVersion(input)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.DevelopmentVersion != "v2.12.0-dev.3" {
		t.Fatalf("development version = %q, want v2.12.0-dev.3", resolved.DevelopmentVersion)
	}
}
