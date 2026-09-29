// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "testing"

func TestStateV3BoundedCompactDecodersAcceptCanonicalShapes(t *testing.T) {
	lease := StateV3ActiveOperationLease{
		SchemaVersion: "1", RepositoryID: "repo", RepositoryFullName: "org/repo", OriginalCommentID: "1",
		Command: "minor", RequestedVersion: "v1.2.0", ResolvedVersion: "v1.2.0", SourceRef: "refs/heads/main",
		SourceOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RequestKey: "request", RequestSHA256: stateV3BoundedDigest,
		ReservationMarker: "marker", VersionResolutionSHA256: stateV3BoundedDigest, CoordinationRef: StateV3CoordinationRef,
		CoordinationClaimPath: "release-lines/1.2.json", CoordinationClaimOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CoordinationClaimBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CoordinationClaimSHA256: stateV3BoundedDigest,
		CoordinationParentOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	claim := StateV3ReleaseLineClaim{
		ReleaseLine: "1.2", RequestKey: "request", RequestSHA256: stateV3BoundedDigest, LaneRef: StateV3MinorStateRef,
		LaneExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ReservationSHA256: stateV3BoundedDigest,
		VersionResolutionSHA256: stateV3BoundedDigest, Command: "minor", ResolvedVersion: "v1.2.0", State: "active", Phase: StateV3PhaseReserved, Attempt: 1,
	}
	arm := StateV3CoordinationMutationArm{
		Operation: "claim_acquire", Ref: StateV3CoordinationRef, ClaimPath: "release-lines/1.2.json", RequestKey: "request",
		ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IntendedClaimSHA256: stateV3BoundedDigest, Attempt: 1,
	}
	outcome := StateV3CoordinationMutationOutcome{
		SchemaVersion: "1", Operation: "claim_acquire", StateRef: StateV3CoordinationRef, ArmPath: stateV3CoordinationArmPath,
		ArmBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmSHA256: stateV3BoundedDigest,
		ArmCommitOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmTreeOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ClaimPath: "release-lines/1.2.json", RequestKey: "request", RequestSHA256: stateV3BoundedDigest, ReleaseLine: "1.2",
		LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ClaimBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ClaimSHA256: stateV3BoundedDigest,
		ClaimCommitOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ClaimTreeOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IntendedClaimSHA256: stateV3BoundedDigest, Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		ObservedRefOID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	cases := []struct {
		name   string
		raw    []byte
		decode func([]byte, *StateV3BoundedWorkspace) error
	}{
		{"lease", mustCanonicalStateV3(lease), func(raw []byte, workspace *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3ActiveLeaseBounded(raw, workspace)
			return err
		}},
		{"claim", mustCanonicalStateV3(claim), func(raw []byte, workspace *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3CoordinationClaimBounded(raw, workspace)
			return err
		}},
		{"arm", mustCanonicalStateV3(arm), func(raw []byte, workspace *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3CoordinationArmBounded(raw, workspace)
			return err
		}},
		{"outcome", mustCanonicalStateV3(outcome), func(raw []byte, workspace *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3CoordinationOutcomeBounded(raw, workspace)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var workspace StateV3BoundedWorkspace
			if err := tc.decode(tc.raw, &workspace); err != nil {
				t.Fatalf("Decode(%s): %v\n%s", tc.name, err, tc.raw)
			}
			if err := tc.decode(append(tc.raw, ' '), &workspace); err == nil {
				t.Fatal("accepted trailing whitespace")
			}
		})
	}
}

func TestStateV3BoundedCompactDecoderAllocations(t *testing.T) {
	lease := mustCanonicalStateV3(StateV3ActiveOperationLease{SchemaVersion: "1", RepositoryID: "repo", RepositoryFullName: "org/repo", OriginalCommentID: "1", Command: "minor", RequestedVersion: "v1.2.0", ResolvedVersion: "v1.2.0", SourceRef: "refs/heads/main", SourceOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RequestKey: "request", RequestSHA256: stateV3BoundedDigest, ReservationMarker: "marker", VersionResolutionSHA256: stateV3BoundedDigest, CoordinationRef: StateV3CoordinationRef, CoordinationClaimPath: "release-lines/1.2.json", CoordinationClaimOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CoordinationClaimBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CoordinationClaimSHA256: stateV3BoundedDigest, CoordinationParentOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	claim := mustCanonicalStateV3(StateV3ReleaseLineClaim{ReleaseLine: "1.2", RequestKey: "request", RequestSHA256: stateV3BoundedDigest, LaneRef: StateV3MinorStateRef, LaneExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ReservationSHA256: stateV3BoundedDigest, VersionResolutionSHA256: stateV3BoundedDigest, Command: "minor", ResolvedVersion: "v1.2.0", State: "active", Phase: StateV3PhaseReserved, Attempt: 1})
	arm := mustCanonicalStateV3(StateV3CoordinationMutationArm{Operation: "claim_acquire", Ref: StateV3CoordinationRef, ClaimPath: "release-lines/1.2.json", RequestKey: "request", ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IntendedClaimSHA256: stateV3BoundedDigest, Attempt: 1})
	outcome := mustCanonicalStateV3(StateV3CoordinationMutationOutcome{SchemaVersion: "1", Operation: "claim_acquire", StateRef: StateV3CoordinationRef, ArmPath: stateV3CoordinationArmPath, ArmBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmSHA256: stateV3BoundedDigest, ArmCommitOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmTreeOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ClaimPath: "release-lines/1.2.json", RequestKey: "request", RequestSHA256: stateV3BoundedDigest, ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ClaimBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ClaimSHA256: stateV3BoundedDigest, ClaimCommitOID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ClaimTreeOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IntendedClaimSHA256: stateV3BoundedDigest, Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, ObservedRefOID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"})
	cases := []struct {
		name   string
		decode func(*StateV3BoundedWorkspace) error
	}{
		{"lease", func(w *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3ActiveLeaseBounded(lease, w)
			return err
		}},
		{"claim", func(w *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3CoordinationClaimBounded(claim, w)
			return err
		}},
		{"arm", func(w *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3CoordinationArmBounded(arm, w)
			return err
		}},
		{"outcome", func(w *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3CoordinationOutcomeBounded(outcome, w)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var workspace StateV3BoundedWorkspace
			if allocations := testing.AllocsPerRun(100, func() {
				if err := tc.decode(&workspace); err != nil {
					t.Fatal(err)
				}
			}); allocations != 0 {
				t.Fatalf("allocations = %v, want 0", allocations)
			}
		})
	}
}

func TestStateV3BoundedCompactDecoderRejectsTypedAndNestedValues(t *testing.T) {
	raw := mustCanonicalStateV3(StateV3CoordinationMutationArm{
		Operation: "claim_acquire", Ref: StateV3CoordinationRef, ClaimPath: "release-lines/1.2.json", RequestKey: "request",
		ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IntendedClaimSHA256: stateV3BoundedDigest, Attempt: 1,
	})
	for _, invalid := range [][]byte{
		[]byte(`{"attempt":"1"}`),
		[]byte(`{"attempt":1,"claim_path":[]}`),
		[]byte(`{"attempt":1,"attempt":1}`),
		[]byte(`{"attempt":1,"claim_path":null}`),
	} {
		var workspace StateV3BoundedWorkspace
		if _, err := DecodeStateV3CoordinationArmBounded(invalid, &workspace); err == nil {
			t.Fatalf("accepted invalid typed document %s", invalid)
		}
	}
	deep := append([]byte(`{"attempt":1,"claim_path":`), []byte(`[[[[[[[[[0]]]]]]]]]`)...)
	deep = append(deep, []byte(`}`)...)
	var workspace StateV3BoundedWorkspace
	if _, err := DecodeStateV3CoordinationArmBounded(deep, &workspace); err == nil {
		t.Fatal("accepted nested array")
	}
	if len(raw) == 0 {
		t.Fatal("empty canonical fixture")
	}
}

const stateV3BoundedDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
