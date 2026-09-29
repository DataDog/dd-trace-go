// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package gardenerrelease_test

import (
	"strings"
	"testing"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

type stateV3BoundedExpectationFixture struct {
	values  []string
	attempt int32
}

func TestStateV3BoundedPredicateExpectationFieldMatrix(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		fixture stateV3BoundedExpectationFixture
		build   func(*testing.T, stateV3BoundedExpectationFixture) any
		decode  func([]byte, *gardenerrelease.StateV3BoundedWorkspace) (any, error)
		matches func(any, any) bool
	}{
		{
			name: "acquire arm",
			raw:  `{"attempt":1,"claim_path":"release-lines/1.2.json","expected_head_oid":"head","intended_claim_sha256":"intent","lane_ref":"lane","operation":"claim_acquire","ref":"coord","release_line":"1.2","request_key":"request","resolved_version":"v1.2.3"}`,
			fixture: stateV3BoundedExpectationFixture{values: []string{
				"coord", "release-lines/1.2.json", "request", "1.2", "lane", "v1.2.3", "head", "intent",
			}, attempt: 1},
			build: stateV3BoundedAcquireArmExpectation,
			decode: func(raw []byte, w *gardenerrelease.StateV3BoundedWorkspace) (any, error) {
				return gardenerrelease.DecodeStateV3CoordinationArmBounded(raw, w)
			},
			matches: func(model, expected any) bool {
				return gardenerrelease.StateV3BoundedArmMatchesClaimAcquire(model.(gardenerrelease.StateV3BoundedCoordinationArm), expected.(gardenerrelease.StateV3BoundedClaimAcquireArmExpectation))
			},
		},
		{
			name: "release arm",
			raw:  `{"attempt":1,"claim_path":"release-lines/1.2.json","expected_claim_blob_oid":"claimblob","expected_head_oid":"head","lane_ref":"lane","operation":"claim_release","ref":"coord","release_line":"1.2","request_key":"request","resolved_version":"v1.2.3"}`,
			fixture: stateV3BoundedExpectationFixture{values: []string{
				"coord", "release-lines/1.2.json", "request", "1.2", "lane", "v1.2.3", "head", "claimblob",
			}, attempt: 1},
			build: stateV3BoundedReleaseArmExpectation,
			decode: func(raw []byte, w *gardenerrelease.StateV3BoundedWorkspace) (any, error) {
				return gardenerrelease.DecodeStateV3CoordinationArmBounded(raw, w)
			},
			matches: func(model, expected any) bool {
				return gardenerrelease.StateV3BoundedArmMatchesClaimRelease(model.(gardenerrelease.StateV3BoundedCoordinationArm), expected.(gardenerrelease.StateV3BoundedClaimReleaseArmExpectation))
			},
		},
		{
			name: "acquire outcome",
			raw:  `{"arm_blob_oid":"armblob","arm_commit_oid":"armcommit","arm_path":"mutation-arm.json","arm_sha256":"armsha","arm_tree_oid":"armtree","claim_path":"release-lines/1.2.json","expected_head_oid":"head","intended_claim_sha256":"intent","lane_ref":"lane","observed_ref_oid":"observedoid","operation":"claim_acquire","release_line":"1.2","request_key":"request","request_sha256":"requestsha","resolved_version":"v1.2.3","response":{"attempts":1,"observation":"observed","oid":"responseoid"},"schema_version":"1","state_ref":"coord"}`,
			fixture: stateV3BoundedExpectationFixture{values: []string{
				"1", "coord", "mutation-arm.json", "armblob", "armsha", "armcommit", "armtree", "release-lines/1.2.json", "request", "requestsha", "1.2", "lane", "v1.2.3", "head", "intent", "responseoid", "observedoid",
			}, attempt: 1},
			build: stateV3BoundedAcquireOutcomeExpectation,
			decode: func(raw []byte, w *gardenerrelease.StateV3BoundedWorkspace) (any, error) {
				return gardenerrelease.DecodeStateV3CoordinationOutcomeBounded(raw, w)
			},
			matches: func(model, expected any) bool {
				return gardenerrelease.StateV3BoundedOutcomeMatchesClaimAcquire(model.(gardenerrelease.StateV3BoundedCoordinationOutcome), expected.(gardenerrelease.StateV3BoundedClaimAcquireOutcomeExpectation))
			},
		},
		{
			name: "release outcome",
			raw:  `{"arm_blob_oid":"armblob","arm_commit_oid":"armcommit","arm_path":"mutation-arm.json","arm_sha256":"armsha","arm_tree_oid":"armtree","claim_blob_oid":"claimblob","claim_commit_oid":"claimcommit","claim_path":"release-lines/1.2.json","claim_sha256":"claimsha","claim_tree_oid":"claimtree","expected_claim_blob_oid":"expectedclaimblob","expected_head_oid":"head","lane_ref":"lane","observed_ref_oid":"observedoid","operation":"claim_release","release_line":"1.2","request_key":"request","request_sha256":"requestsha","resolved_version":"v1.2.3","response":{"attempts":1,"observation":"observed","oid":"responseoid"},"schema_version":"1","state_ref":"coord"}`,
			fixture: stateV3BoundedExpectationFixture{values: []string{
				"1", "coord", "mutation-arm.json", "armblob", "armsha", "armcommit", "armtree", "release-lines/1.2.json", "request", "requestsha", "1.2", "lane", "v1.2.3", "head", "claimblob", "claimsha", "claimcommit", "claimtree", "expectedclaimblob", "responseoid", "observedoid",
			}, attempt: 1},
			build: stateV3BoundedReleaseOutcomeExpectation,
			decode: func(raw []byte, w *gardenerrelease.StateV3BoundedWorkspace) (any, error) {
				return gardenerrelease.DecodeStateV3CoordinationOutcomeBounded(raw, w)
			},
			matches: func(model, expected any) bool {
				return gardenerrelease.StateV3BoundedOutcomeMatchesClaimRelease(model.(gardenerrelease.StateV3BoundedCoordinationOutcome), expected.(gardenerrelease.StateV3BoundedClaimReleaseOutcomeExpectation))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var workspace gardenerrelease.StateV3BoundedWorkspace
			model, err := tc.decode([]byte(tc.raw), &workspace)
			if err != nil {
				t.Fatal(err)
			}
			expected := tc.build(t, tc.fixture)
			if !tc.matches(model, expected) {
				t.Fatal("valid expectation did not match")
			}
			if allocations := testing.AllocsPerRun(100, func() {
				if !tc.matches(model, expected) {
					t.Fatal("valid expectation did not match")
				}
			}); allocations != 0 {
				t.Fatalf("predicate allocations = %v, want 0", allocations)
			}
			for field := range tc.fixture.values {
				mismatch := tc.fixture
				mismatch.values = append([]string(nil), tc.fixture.values...)
				mismatch.values[field] = "mismatch"
				if tc.matches(model, tc.build(t, mismatch)) {
					t.Fatalf("mismatch field %d matched", field)
				}
			}
			mismatch := tc.fixture
			mismatch.attempt++
			if tc.matches(model, tc.build(t, mismatch)) {
				t.Fatal("mismatch attempt matched")
			}
			if strings.Contains(tc.name, "outcome") {
				attemptsTwo := strings.Replace(tc.raw, `"attempts":1`, `"attempts":2`, 1)
				attemptsTwoModel, err := tc.decode([]byte(attemptsTwo), &workspace)
				if err != nil {
					t.Fatal(err)
				}
				if tc.matches(attemptsTwoModel, tc.build(t, mismatch)) {
					t.Fatal("two-attempt observed outcome matched caller expected two attempts")
				}
			}
		})
	}
}

func stateV3BoundedAcquireArmExpectation(t *testing.T, f stateV3BoundedExpectationFixture) any {
	t.Helper()
	expected, ok := gardenerrelease.NewStateV3BoundedClaimAcquireArmExpectation(stateV3BoundedBytes(f.values[0]), stateV3BoundedBytes(f.values[1]), stateV3BoundedBytes(f.values[2]), stateV3BoundedBytes(f.values[3]), stateV3BoundedBytes(f.values[4]), stateV3BoundedBytes(f.values[5]), stateV3BoundedBytes(f.values[6]), stateV3BoundedBytes(f.values[7]), f.attempt)
	if !ok {
		t.Fatal("expectation constructor rejected fixture")
	}
	return expected
}

func stateV3BoundedReleaseArmExpectation(t *testing.T, f stateV3BoundedExpectationFixture) any {
	t.Helper()
	expected, ok := gardenerrelease.NewStateV3BoundedClaimReleaseArmExpectation(stateV3BoundedBytes(f.values[0]), stateV3BoundedBytes(f.values[1]), stateV3BoundedBytes(f.values[2]), stateV3BoundedBytes(f.values[3]), stateV3BoundedBytes(f.values[4]), stateV3BoundedBytes(f.values[5]), stateV3BoundedBytes(f.values[6]), stateV3BoundedBytes(f.values[7]), f.attempt)
	if !ok {
		t.Fatal("expectation constructor rejected fixture")
	}
	return expected
}

func stateV3BoundedAcquireOutcomeExpectation(t *testing.T, f stateV3BoundedExpectationFixture) any {
	t.Helper()
	expected, ok := gardenerrelease.NewStateV3BoundedClaimAcquireOutcomeExpectation(stateV3BoundedBytes(f.values[0]), stateV3BoundedBytes(f.values[1]), stateV3BoundedBytes(f.values[2]), stateV3BoundedBytes(f.values[3]), stateV3BoundedBytes(f.values[4]), stateV3BoundedBytes(f.values[5]), stateV3BoundedBytes(f.values[6]), stateV3BoundedBytes(f.values[7]), stateV3BoundedBytes(f.values[8]), stateV3BoundedBytes(f.values[9]), stateV3BoundedBytes(f.values[10]), stateV3BoundedBytes(f.values[11]), stateV3BoundedBytes(f.values[12]), stateV3BoundedBytes(f.values[13]), stateV3BoundedBytes(f.values[14]), stateV3BoundedBytes(f.values[15]), stateV3BoundedBytes(f.values[16]), f.attempt)
	if !ok {
		t.Fatal("expectation constructor rejected fixture")
	}
	return expected
}

func stateV3BoundedReleaseOutcomeExpectation(t *testing.T, f stateV3BoundedExpectationFixture) any {
	t.Helper()
	expected, ok := gardenerrelease.NewStateV3BoundedClaimReleaseOutcomeExpectation(stateV3BoundedBytes(f.values[0]), stateV3BoundedBytes(f.values[1]), stateV3BoundedBytes(f.values[2]), stateV3BoundedBytes(f.values[3]), stateV3BoundedBytes(f.values[4]), stateV3BoundedBytes(f.values[5]), stateV3BoundedBytes(f.values[6]), stateV3BoundedBytes(f.values[7]), stateV3BoundedBytes(f.values[8]), stateV3BoundedBytes(f.values[9]), stateV3BoundedBytes(f.values[10]), stateV3BoundedBytes(f.values[11]), stateV3BoundedBytes(f.values[12]), stateV3BoundedBytes(f.values[13]), stateV3BoundedBytes(f.values[14]), stateV3BoundedBytes(f.values[15]), stateV3BoundedBytes(f.values[16]), stateV3BoundedBytes(f.values[17]), stateV3BoundedBytes(f.values[18]), stateV3BoundedBytes(f.values[19]), stateV3BoundedBytes(f.values[20]), f.attempt)
	if !ok {
		t.Fatal("expectation constructor rejected fixture")
	}
	return expected
}

func stateV3BoundedBytes(value string) []byte { return []byte(value) }
