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

const stateV3BoundedActiveClaimRaw = `{"attempt":1,"command":"release:prepare","lane_expected_head_oid":"head","lane_ref":"refs/heads/v1.2.x","phase":"reserved","release_line":"1.2","request_key":"request","request_sha256":"requestsha","reservation_sha256":"reservationsha","resolved_version":"v1.2.3","state":"active","version_resolution_sha256":"versionsha"}`

func stateV3BoundedActiveClaimExpectation(t *testing.T, developmentVersion []byte, present bool) gardenerrelease.StateV3BoundedActiveClaimExpectation {
	t.Helper()
	expected, ok := gardenerrelease.NewStateV3BoundedActiveClaimExpectation(
		[]byte("release:prepare"), developmentVersion, present, []byte("head"), []byte("refs/heads/v1.2.x"), []byte("1.2"), []byte("request"), []byte("requestsha"), []byte("reservationsha"), []byte("v1.2.3"), []byte("versionsha"),
	)
	if !ok {
		t.Fatal("expectation constructor rejected valid fields")
	}
	return expected
}

func TestStateV3BoundedActiveClaimPredicate(t *testing.T) {
	if !gardenerrelease.ValidateStateV3ReleaseLineClaimDocument([]byte(stateV3BoundedActiveClaimRaw)) {
		t.Fatal("fixture is not a dynamic canonical claim")
	}
	var workspace gardenerrelease.StateV3BoundedWorkspace
	claim, err := gardenerrelease.DecodeStateV3CoordinationClaimBounded([]byte(stateV3BoundedActiveClaimRaw), &workspace)
	if err != nil {
		t.Fatal(err)
	}
	expected := stateV3BoundedActiveClaimExpectation(t, nil, false)
	if !gardenerrelease.StateV3BoundedClaimMatchesActiveReservation(claim, expected) {
		t.Fatal("exact active claim did not match")
	}
	if allocations := testing.AllocsPerRun(100, func() {
		if !gardenerrelease.StateV3BoundedClaimMatchesActiveReservation(claim, expected) {
			t.Fatal("exact active claim did not match")
		}
	}); allocations != 0 {
		t.Fatalf("predicate allocations = %v, want 0", allocations)
	}
	for _, mutation := range []struct{ old, new string }{
		{`"attempt":1`, `"attempt":2`},
		{`"command":"release:prepare"`, `"command":"release:promote"`},
		{`"lane_expected_head_oid":"head"`, `"lane_expected_head_oid":"other"`},
		{`"lane_ref":"refs/heads/v1.2.x"`, `"lane_ref":"other"`},
		{`"phase":"reserved"`, `"phase":"prepared"`},
		{`"release_line":"1.2"`, `"release_line":"2.0"`},
		{`"request_key":"request"`, `"request_key":"other"`},
		{`"request_sha256":"requestsha"`, `"request_sha256":"other"`},
		{`"reservation_sha256":"reservationsha"`, `"reservation_sha256":"other"`},
		{`"resolved_version":"v1.2.3"`, `"resolved_version":"v9.9.9"`},
		{`"state":"active"`, `"state":"inactive"`},
		{`"version_resolution_sha256":"versionsha"`, `"version_resolution_sha256":"other"`},
	} {
		raw := strings.Replace(stateV3BoundedActiveClaimRaw, mutation.old, mutation.new, 1)
		candidate, err := gardenerrelease.DecodeStateV3CoordinationClaimBounded([]byte(raw), &workspace)
		if err != nil {
			t.Fatalf("decode mutation %q: %v", mutation.new, err)
		}
		if gardenerrelease.StateV3BoundedClaimMatchesActiveReservation(candidate, expected) {
			t.Fatalf("mutation %q matched", mutation.new)
		}
	}
}

func TestStateV3BoundedActiveClaimExpectationCopiesInput(t *testing.T) {
	command := []byte("release:prepare")
	expected, ok := gardenerrelease.NewStateV3BoundedActiveClaimExpectation(command, nil, false, []byte("head"), []byte("refs/heads/v1.2.x"), []byte("1.2"), []byte("request"), []byte("requestsha"), []byte("reservationsha"), []byte("v1.2.3"), []byte("versionsha"))
	if !ok {
		t.Fatal("expectation constructor rejected valid fields")
	}
	command[0] = 'x'
	var workspace gardenerrelease.StateV3BoundedWorkspace
	claim, err := gardenerrelease.DecodeStateV3CoordinationClaimBounded([]byte(stateV3BoundedActiveClaimRaw), &workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !gardenerrelease.StateV3BoundedClaimMatchesActiveReservation(claim, expected) {
		t.Fatal("input mutation changed expectation")
	}
}

func TestStateV3BoundedActiveClaimOptionalDevelopmentVersion(t *testing.T) {
	withDevelopment := strings.Replace(stateV3BoundedActiveClaimRaw, `"lane_expected_head_oid"`, `"development_version":"v1.2.4-dev","lane_expected_head_oid"`, 1)
	withEmptyDevelopment := strings.Replace(stateV3BoundedActiveClaimRaw, `"lane_expected_head_oid"`, `"development_version":"","lane_expected_head_oid"`, 1)
	var workspace gardenerrelease.StateV3BoundedWorkspace
	claim, err := gardenerrelease.DecodeStateV3CoordinationClaimBounded([]byte(withDevelopment), &workspace)
	if err != nil {
		t.Fatal(err)
	}
	presentExpectation := stateV3BoundedActiveClaimExpectation(t, []byte("v1.2.4-dev"), true)
	absentExpectation := stateV3BoundedActiveClaimExpectation(t, nil, false)
	if !gardenerrelease.StateV3BoundedClaimMatchesActiveReservation(claim, presentExpectation) {
		t.Fatal("matching development version did not match")
	}
	if gardenerrelease.StateV3BoundedClaimMatchesActiveReservation(claim, absentExpectation) {
		t.Fatal("present development version matched absent expectation")
	}
	if gardenerrelease.StateV3BoundedClaimMatchesActiveReservation(claim, stateV3BoundedActiveClaimExpectation(t, []byte("other"), true)) {
		t.Fatal("different development version matched")
	}
	absent, err := gardenerrelease.DecodeStateV3CoordinationClaimBounded([]byte(stateV3BoundedActiveClaimRaw), &workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !gardenerrelease.StateV3BoundedClaimMatchesActiveReservation(absent, absentExpectation) {
		t.Fatal("absent development version did not match absent expectation")
	}
	if gardenerrelease.StateV3BoundedClaimMatchesActiveReservation(absent, presentExpectation) {
		t.Fatal("absent development version matched present expectation")
	}
	if _, err := gardenerrelease.DecodeStateV3CoordinationClaimBounded([]byte(withEmptyDevelopment), &workspace); err == nil {
		t.Fatal("present empty development version decoded")
	}
	if _, ok := gardenerrelease.NewStateV3BoundedActiveClaimExpectation([]byte("release:prepare"), nil, true, []byte("head"), []byte("refs/heads/v1.2.x"), []byte("1.2"), []byte("request"), []byte("requestsha"), []byte("reservationsha"), []byte("v1.2.3"), []byte("versionsha")); ok {
		t.Fatal("constructor accepted present empty development version")
	}
}
