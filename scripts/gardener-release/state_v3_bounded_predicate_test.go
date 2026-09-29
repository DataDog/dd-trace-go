// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package gardenerrelease_test

import (
	"testing"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

func TestStateV3BoundedArmPredicateCrossPackage(t *testing.T) {
	raw := []byte(`{"attempt":1,"claim_path":"release-lines/1.2.json","expected_head_oid":"1111111111111111111111111111111111111111","intended_claim_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","lane_ref":"refs/heads/v1.2.x","operation":"claim_acquire","ref":"refs/heads/gardener-release-coordination","release_line":"1.2","request_key":"request","resolved_version":"v1.2.3"}`)
	var workspace gardenerrelease.StateV3BoundedWorkspace
	arm, err := gardenerrelease.DecodeStateV3CoordinationArmBounded(raw, &workspace)
	if err != nil {
		t.Fatal(err)
	}
	expected, ok := gardenerrelease.NewStateV3BoundedClaimAcquireArmExpectation(
		[]byte("refs/heads/gardener-release-coordination"), []byte("release-lines/1.2.json"), []byte("request"), []byte("1.2"), []byte("refs/heads/v1.2.x"), []byte("v1.2.3"), []byte("1111111111111111111111111111111111111111"), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 1,
	)
	if !ok {
		t.Fatal("expectation constructor rejected valid values")
	}
	if !gardenerrelease.StateV3BoundedArmMatchesClaimAcquire(arm, expected) {
		t.Fatal("expected exact claim-acquire predicate match")
	}
	if gardenerrelease.StateV3BoundedArmMatchesClaimRelease(arm, gardenerrelease.StateV3BoundedClaimReleaseArmExpectation{}) {
		t.Fatal("claim-acquire arm matched claim-release predicate")
	}
	expected, ok = gardenerrelease.NewStateV3BoundedClaimAcquireArmExpectation(
		[]byte("refs/heads/gardener-release-coordination"), []byte("release-lines/1.2.json"), []byte("request"), []byte("1.2"), []byte("refs/heads/v1.2.x"), []byte("v1.2.3"), []byte("1111111111111111111111111111111111111111"), []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), 2,
	)
	if !ok {
		t.Fatal("expectation constructor rejected valid values")
	}
	if gardenerrelease.StateV3BoundedArmMatchesClaimAcquire(arm, expected) {
		t.Fatal("wrong attempt matched")
	}
}
