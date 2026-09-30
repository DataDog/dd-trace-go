// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"testing"
)

func TestStateV3BoundedCompactDecodersRejectNegativeZero(t *testing.T) {
	claim := mustCanonicalStateV3(StateV3ReleaseLineClaim{
		Attempt: 0, Command: "minor", LaneExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LaneRef: StateV3MinorStateRef,
		Phase: StateV3PhaseReserved, ReleaseLine: "1.2", RequestKey: "request", RequestSHA256: stateV3BoundedDigest,
		ReservationSHA256: stateV3BoundedDigest, ResolvedVersion: "v1.2.0", State: "active", VersionResolutionSHA256: stateV3BoundedDigest,
	})
	arm := mustCanonicalStateV3(StateV3CoordinationMutationArm{
		Attempt: 0, ClaimPath: "release-lines/1.2.json", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IntendedClaimSHA256: stateV3BoundedDigest, LaneRef: StateV3MinorStateRef, Operation: "claim_acquire", Ref: StateV3CoordinationRef,
		ReleaseLine: "1.2", RequestKey: "request", ResolvedVersion: "v1.2.0",
	})
	outcome := mustCanonicalStateV3(StateV3CoordinationMutationOutcome{
		SchemaVersion: "1", Operation: "claim_acquire", StateRef: StateV3CoordinationRef, ArmPath: stateV3CoordinationArmPath,
		ArmBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmSHA256: stateV3BoundedDigest,
		ArmCommitOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmTreeOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ClaimPath: "release-lines/1.2.json", ClaimBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ClaimSHA256: stateV3BoundedDigest,
		ClaimCommitOID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ClaimTreeOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IntendedClaimSHA256: stateV3BoundedDigest,
		LaneRef: StateV3MinorStateRef, ObservedRefOID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ReleaseLine: "1.2",
		RequestKey: "request", RequestSHA256: stateV3BoundedDigest, ResolvedVersion: "v1.2.0",
		Response: StateV3MutationResponse{Attempts: 0, Observation: "observed", OID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	})

	cases := []struct {
		name     string
		zero     []byte
		negative []byte
		dynamic  func([]byte) bool
		bounded  func([]byte, *StateV3BoundedWorkspace) error
	}{
		{
			name:     "claim-attempt",
			zero:     claim,
			negative: bytes.Replace(claim, []byte(`"attempt":0`), []byte(`"attempt":-0`), 1),
			dynamic:  ValidateStateV3ReleaseLineClaimDocument,
			bounded: func(raw []byte, workspace *StateV3BoundedWorkspace) error {
				_, err := DecodeStateV3CoordinationClaimBounded(raw, workspace)
				return err
			},
		},
		{
			name:     "arm-attempt",
			zero:     arm,
			negative: bytes.Replace(arm, []byte(`"attempt":0`), []byte(`"attempt":-0`), 1),
			dynamic:  ValidateStateV3CoordinationArmDocument,
			bounded: func(raw []byte, workspace *StateV3BoundedWorkspace) error {
				_, err := DecodeStateV3CoordinationArmBounded(raw, workspace)
				return err
			},
		},
		{
			name:     "outcome-response-attempts",
			zero:     outcome,
			negative: bytes.Replace(outcome, []byte(`"attempts":0`), []byte(`"attempts":-0`), 1),
			dynamic: func(raw []byte) bool {
				var value StateV3CoordinationMutationOutcome
				return decodeStateV3Document(raw, MaxStateV3CoordinationOutcomeBytes, &value) == nil && bytes.Equal(mustCanonicalStateV3(value), raw)
			},
			bounded: func(raw []byte, workspace *StateV3BoundedWorkspace) error {
				_, err := DecodeStateV3CoordinationOutcomeBounded(raw, workspace)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.dynamic(tc.zero) {
				t.Fatal("dynamic canonical validation rejected zero")
			}
			var workspace StateV3BoundedWorkspace
			if err := tc.bounded(tc.zero, &workspace); err != nil {
				t.Fatalf("bounded decoder rejected zero: %v", err)
			}
			if tc.dynamic(tc.negative) {
				t.Fatal("dynamic canonical validation accepted negative zero")
			}
			if err := tc.bounded(tc.negative, &workspace); err == nil {
				t.Fatal("bounded decoder accepted negative zero")
			}
		})
	}
}
