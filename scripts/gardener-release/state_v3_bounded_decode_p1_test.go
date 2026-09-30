// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"testing"
)

func TestStateV3BoundedCompactDecodersRequireCanonicalStringSpelling(t *testing.T) {
	arm := StateV3CoordinationMutationArm{
		Operation: "claim_acquire", Ref: StateV3CoordinationRef, ClaimPath: "release-lines/1.2.json", RequestKey: "a<&>\n",
		ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IntendedClaimSHA256: stateV3BoundedDigest, Attempt: 1,
	}
	raw := mustCanonicalStateV3(arm)
	if !bytes.Contains(raw, []byte(`\u003c`)) || !bytes.Contains(raw, []byte(`\n`)) {
		t.Fatalf("fixture did not contain canonical escapes: %s", raw)
	}
	invalid := [][]byte{
		bytes.Replace(raw, []byte(`\u003c`), []byte(`<`), 1),
		bytes.Replace(raw, []byte(`\u003e`), []byte(`\u003E`), 1),
		bytes.Replace(raw, []byte(`\n`), []byte(`\u000a`), 1),
		bytes.Replace(raw, []byte(`"claim_acquire"`), []byte(`"claim\/acquire"`), 1),
	}
	for _, candidate := range invalid {
		var workspace StateV3BoundedWorkspace
		if _, err := DecodeStateV3CoordinationArmBounded(candidate, &workspace); err == nil {
			t.Fatalf("accepted noncanonical spelling %s", candidate)
		}
	}
}

func TestStateV3BoundedCompactDecodersRejectOutcomeResponseMutations(t *testing.T) {
	raw := mustCanonicalStateV3(StateV3CoordinationMutationOutcome{
		SchemaVersion: "1", Operation: "claim_acquire", StateRef: StateV3CoordinationRef, ArmPath: stateV3CoordinationArmPath,
		ArmBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmSHA256: stateV3BoundedDigest,
		ArmCommitOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmTreeOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ClaimPath: "release-lines/1.2.json", RequestKey: "request", RequestSHA256: stateV3BoundedDigest, ReleaseLine: "1.2",
		LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IntendedClaimSHA256: stateV3BoundedDigest, Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		ObservedRefOID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	mutations := [][]byte{
		bytes.Replace(raw, []byte(`"observation":"observed"`), []byte(`"observation":null`), 1),
		bytes.Replace(raw, []byte(`"oid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`), []byte(`"oid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","extra":"x"`), 1),
		bytes.Replace(raw, []byte(`"attempts":1,"observation"`), []byte(`"attempts":1,"attempts":1,"observation"`), 1),
		bytes.Replace(raw, []byte(`"attempts":1,"observation"`), []byte(`"observation":"observed","attempts":1,"observation"`), 1),
	}
	var largeWorkspace StateV3BoundedWorkspace
	largeAttempts := bytes.Replace(raw, []byte(`"attempts":1`), []byte(`"attempts":4294967296`), 1)
	if _, err := DecodeStateV3CoordinationOutcomeBounded(largeAttempts, &largeWorkspace); err == nil {
		t.Fatal("accepted integer outside the portable signed 32-bit document range")
	}
	for _, candidate := range mutations {
		var workspace StateV3BoundedWorkspace
		if _, err := DecodeStateV3CoordinationOutcomeBounded(candidate, &workspace); err == nil {
			t.Fatalf("accepted outcome mutation %s", candidate)
		}
	}
}

func TestStateV3BoundedCompactDecodersRejectSizeAndInvalidUTF8(t *testing.T) {
	var workspace StateV3BoundedWorkspace
	if _, err := DecodeStateV3CoordinationArmBounded(make([]byte, stateV3BoundedCompactDocumentBytes+1), &workspace); err == nil {
		t.Fatal("accepted document larger than 16KiB")
	}
	raw := mustCanonicalStateV3(StateV3CoordinationMutationArm{Operation: "claim_acquire", Ref: StateV3CoordinationRef, ClaimPath: "release-lines/1.2.json", RequestKey: "request", ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IntendedClaimSHA256: stateV3BoundedDigest, Attempt: 1})
	invalidUTF8 := bytes.Replace(raw, []byte(`"request"`), []byte{'"', 0xff, '"'}, 1)
	if _, err := DecodeStateV3CoordinationArmBounded(invalidUTF8, &workspace); err == nil {
		t.Fatal("accepted invalid UTF-8")
	}
}

func TestStateV3BoundedActiveLeaseDevelopmentVersionCanonicalParity(t *testing.T) {
	lease := StateV3ActiveOperationLease{
		SchemaVersion: "1", RepositoryID: "repo", RepositoryFullName: "org/repo", OriginalCommentID: "1",
		Command: "minor", RequestedVersion: "v1.2.0", ResolvedVersion: "v1.2.0", SourceRef: "refs/heads/main",
		SourceOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RequestKey: "request", RequestSHA256: stateV3BoundedDigest,
		ReservationMarker: "marker", VersionResolutionSHA256: stateV3BoundedDigest, CoordinationRef: StateV3CoordinationRef,
		CoordinationClaimPath: "release-lines/1.2.json", CoordinationClaimOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CoordinationClaimBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CoordinationClaimSHA256: stateV3BoundedDigest,
		CoordinationParentOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	absent := mustCanonicalStateV3(lease)
	lease.DevelopmentVersion = "v1.2.4-dev"
	present := mustCanonicalStateV3(lease)
	empty := bytes.Replace(absent, []byte(`"original_comment_id"`), []byte(`"development_version":"","original_comment_id"`), 1)
	if bytes.Equal(absent, empty) {
		t.Fatal("fixture did not add empty development_version")
	}

	cases := []struct {
		name  string
		raw   []byte
		valid bool
	}{
		{"absent", absent, true},
		{"present", present, true},
		{"present-empty", empty, false},
		{"present-null", bytes.Replace(present, []byte(`"development_version":"v1.2.4-dev"`), []byte(`"development_version":null`), 1), false},
		{"present-array", bytes.Replace(present, []byte(`"development_version":"v1.2.4-dev"`), []byte(`"development_version":[]`), 1), false},
		{"present-duplicate", bytes.Replace(present, []byte(`"development_version":"v1.2.4-dev","original_comment_id"`), []byte(`"development_version":"v1.2.4-dev","development_version":"v1.2.4-dev","original_comment_id"`), 1), false},
		{"present-out-of-order", bytes.Replace(present, []byte(`"development_version":"v1.2.4-dev","original_comment_id":"1"`), []byte(`"original_comment_id":"1","development_version":"v1.2.4-dev"`), 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidateStateV3ActiveLeaseDocument(tc.raw); got != tc.valid {
				t.Fatalf("dynamic canonical validation = %v, want %v", got, tc.valid)
			}
			var workspace StateV3BoundedWorkspace
			_, err := DecodeStateV3ActiveLeaseBounded(tc.raw, &workspace)
			if got := err == nil; got != tc.valid {
				t.Fatalf("bounded decode success = %v, want %v (err = %v)", got, tc.valid, err)
			}
		})
	}
}

func TestStateV3BoundedCompactDecodersAgreeWithDynamicCanonicalValidation(t *testing.T) {
	lease := StateV3ActiveOperationLease{SchemaVersion: "1", RepositoryID: "repo", RepositoryFullName: "org/repo", OriginalCommentID: "1", Command: "minor", RequestedVersion: "v1.2.0", ResolvedVersion: "v1.2.0", SourceRef: "refs/heads/main", SourceOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RequestKey: "request", RequestSHA256: stateV3BoundedDigest, ReservationMarker: "marker", VersionResolutionSHA256: stateV3BoundedDigest, CoordinationRef: StateV3CoordinationRef, CoordinationClaimPath: "release-lines/1.2.json", CoordinationClaimOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CoordinationClaimBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CoordinationClaimSHA256: stateV3BoundedDigest, CoordinationParentOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	claim := StateV3ReleaseLineClaim{ReleaseLine: "1.2", RequestKey: "request", RequestSHA256: stateV3BoundedDigest, LaneRef: StateV3MinorStateRef, LaneExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ReservationSHA256: stateV3BoundedDigest, VersionResolutionSHA256: stateV3BoundedDigest, Command: "minor", ResolvedVersion: "v1.2.0", State: "active", Phase: StateV3PhaseReserved, Attempt: 1}
	arm := StateV3CoordinationMutationArm{Operation: "claim_acquire", Ref: StateV3CoordinationRef, ClaimPath: "release-lines/1.2.json", RequestKey: "request", ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IntendedClaimSHA256: stateV3BoundedDigest, Attempt: 1}
	outcome := StateV3CoordinationMutationOutcome{SchemaVersion: "1", Operation: "claim_acquire", StateRef: StateV3CoordinationRef, ArmPath: stateV3CoordinationArmPath, ArmBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmSHA256: stateV3BoundedDigest, ArmCommitOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmTreeOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ClaimPath: "release-lines/1.2.json", RequestKey: "request", RequestSHA256: stateV3BoundedDigest, ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ClaimBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ClaimSHA256: stateV3BoundedDigest, ClaimCommitOID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ClaimTreeOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IntendedClaimSHA256: stateV3BoundedDigest, Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, ObservedRefOID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	cases := []struct {
		name    string
		raw     []byte
		dynamic bool
		bounded func([]byte, *StateV3BoundedWorkspace) error
	}{
		{"lease", mustCanonicalStateV3(lease), ValidateStateV3ActiveLeaseDocument(mustCanonicalStateV3(lease)), func(r []byte, w *StateV3BoundedWorkspace) error {
			_, e := DecodeStateV3ActiveLeaseBounded(r, w)
			return e
		}},
		{"claim", mustCanonicalStateV3(claim), ValidateStateV3ReleaseLineClaimDocument(mustCanonicalStateV3(claim)), func(r []byte, w *StateV3BoundedWorkspace) error {
			_, e := DecodeStateV3CoordinationClaimBounded(r, w)
			return e
		}},
		{"arm", mustCanonicalStateV3(arm), ValidateStateV3CoordinationArmDocument(mustCanonicalStateV3(arm)), func(r []byte, w *StateV3BoundedWorkspace) error {
			_, e := DecodeStateV3CoordinationArmBounded(r, w)
			return e
		}},
		// Outcome lifecycle validity intentionally remains outside structural
		// bounded decoding. Require only the dynamic canonical structural round trip.
		{"outcome", mustCanonicalStateV3(outcome), func() bool {
			var value StateV3CoordinationMutationOutcome
			raw := mustCanonicalStateV3(outcome)
			return decodeStateV3Document(raw, MaxStateV3CoordinationOutcomeBytes, &value) == nil && bytes.Equal(mustCanonicalStateV3(value), raw)
		}(), func(r []byte, w *StateV3BoundedWorkspace) error {
			_, e := DecodeStateV3CoordinationOutcomeBounded(r, w)
			return e
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var workspace StateV3BoundedWorkspace
			if !tc.dynamic {
				t.Fatal("dynamic canonical validation rejected fixture")
			}
			if err := tc.bounded(tc.raw, &workspace); err != nil {
				t.Fatalf("bounded canonical decode rejected fixture: %v", err)
			}
			for _, candidate := range [][]byte{append([]byte(" "), tc.raw...), append(append([]byte(nil), tc.raw...), ' '), bytes.Replace(tc.raw, []byte(`:`), []byte(` :`), 1), bytes.Replace(tc.raw, []byte(`,`), []byte(`,,`), 1)} {
				if tc.bounded(candidate, &workspace) == nil {
					t.Fatalf("accepted noncanonical mutation %q", candidate)
				}
			}
		})
	}
}
