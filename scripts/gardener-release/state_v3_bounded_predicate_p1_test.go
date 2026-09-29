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

func TestStateV3BoundedArmMatchesClaimReleaseCrossPackage(t *testing.T) {
	raw := []byte(`{"attempt":1,"claim_path":"release-lines/1.2.json","expected_claim_blob_oid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expected_head_oid":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","lane_ref":"refs/heads/v1.2.x","operation":"claim_release","ref":"refs/heads/gardener-release-coordination","release_line":"1.2","request_key":"request","resolved_version":"v1.2.3"}`)
	var workspace gardenerrelease.StateV3BoundedWorkspace
	arm, err := gardenerrelease.DecodeStateV3CoordinationArmBounded(raw, &workspace)
	if err != nil {
		t.Fatal(err)
	}
	expected, ok := gardenerrelease.NewStateV3BoundedClaimReleaseArmExpectation(
		[]byte("refs/heads/gardener-release-coordination"), []byte("release-lines/1.2.json"), []byte("request"), []byte("1.2"), []byte("refs/heads/v1.2.x"), []byte("v1.2.3"), []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 1,
	)
	if !ok {
		t.Fatal("expectation constructor rejected valid values")
	}
	if !gardenerrelease.StateV3BoundedArmMatchesClaimRelease(arm, expected) {
		t.Fatal("expected exact claim-release predicate match")
	}
	expected, ok = gardenerrelease.NewStateV3BoundedClaimReleaseArmExpectation(
		[]byte("refs/heads/gardener-release-coordination"), []byte("release-lines/1.2.json"), []byte("request"), []byte("1.2"), []byte("refs/heads/other"), []byte("v1.2.3"), []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 1,
	)
	if !ok {
		t.Fatal("expectation constructor rejected valid values")
	}
	if gardenerrelease.StateV3BoundedArmMatchesClaimRelease(arm, expected) {
		t.Fatal("wrong lane_ref matched claim-release arm")
	}
}

func TestStateV3BoundedExpectationConstructorCopiesInput(t *testing.T) {
	ref := []byte("refs/heads/gardener-release-coordination")
	expected, ok := gardenerrelease.NewStateV3BoundedClaimAcquireArmExpectation(
		ref, []byte("release-lines/1.2.json"), []byte("request"), []byte("1.2"), []byte("refs/heads/v1.2.x"), []byte("v1.2.3"), []byte("1111111111111111111111111111111111111111"), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 1,
	)
	if !ok {
		t.Fatal("expectation constructor rejected valid values")
	}
	ref[0] = 'x'
	raw := []byte(`{"attempt":1,"claim_path":"release-lines/1.2.json","expected_head_oid":"1111111111111111111111111111111111111111","intended_claim_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","lane_ref":"refs/heads/v1.2.x","operation":"claim_acquire","ref":"refs/heads/gardener-release-coordination","release_line":"1.2","request_key":"request","resolved_version":"v1.2.3"}`)
	var workspace gardenerrelease.StateV3BoundedWorkspace
	arm, err := gardenerrelease.DecodeStateV3CoordinationArmBounded(raw, &workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !gardenerrelease.StateV3BoundedArmMatchesClaimAcquire(arm, expected) {
		t.Fatal("expectation retained an alias to constructor input")
	}
}

func TestStateV3BoundedExpectationConstructorAcceptsSingleArenaSizedValue(t *testing.T) {
	value := []byte(strings.Repeat("x", 16*1024))
	if _, ok := gardenerrelease.NewStateV3BoundedClaimAcquireArmExpectation(value, nil, nil, nil, nil, nil, nil, nil, 1); !ok {
		t.Fatal("16KiB expectation was rejected")
	}
	if _, ok := gardenerrelease.NewStateV3BoundedClaimAcquireArmExpectation(value, []byte("x"), nil, nil, nil, nil, nil, nil, 1); ok {
		t.Fatal("expectation larger than its single arena was accepted")
	}
}
