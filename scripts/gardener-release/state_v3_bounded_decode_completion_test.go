// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func TestStateV3BoundedCompactDecoderDocumentByteBoundaries(t *testing.T) {
	arm := StateV3CoordinationMutationArm{
		Operation: "claim_acquire", Ref: StateV3CoordinationRef, ClaimPath: "release-lines/1.2.json",
		RequestKey: "", ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0",
		ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IntendedClaimSHA256: stateV3BoundedDigest,
		Attempt: 1,
	}
	base := mustCanonicalStateV3(arm)
	if len(base) >= stateV3BoundedCompactDocumentBytes {
		t.Fatalf("base canonical arm length = %d, want < %d", len(base), stateV3BoundedCompactDocumentBytes)
	}
	arm.RequestKey = strings.Repeat("x", stateV3BoundedCompactDocumentBytes-len(base))
	atLimit := mustCanonicalStateV3(arm)
	if len(atLimit) != stateV3BoundedCompactDocumentBytes {
		t.Fatalf("canonical arm length = %d, want %d", len(atLimit), stateV3BoundedCompactDocumentBytes)
	}
	var workspace StateV3BoundedWorkspace
	if _, err := DecodeStateV3CoordinationArmBounded(atLimit, &workspace); err != nil {
		t.Fatalf("Decode at limit: %v", err)
	}
	arm.RequestKey += "x"
	overLimit := mustCanonicalStateV3(arm)
	if len(overLimit) != stateV3BoundedCompactDocumentBytes+1 {
		t.Fatalf("oversize canonical arm length = %d, want %d", len(overLimit), stateV3BoundedCompactDocumentBytes+1)
	}
	if _, err := DecodeStateV3CoordinationArmBounded(overLimit, &workspace); err == nil {
		t.Fatal("accepted canonical document larger than the fixed 16KiB limit")
	}
}

func TestStateV3BoundedCompactModelsResetAndWorkspaceReuseDoNotAlias(t *testing.T) {
	firstRaw := boundedCanonicalArm(t, "first")
	secondRaw := boundedCanonicalArm(t, "second")
	var workspace StateV3BoundedWorkspace
	first, err := DecodeStateV3CoordinationArmBounded(firstRaw, &workspace)
	if err != nil {
		t.Fatalf("Decode first: %v", err)
	}
	firstUsed := first.document.used
	firstArena := first.document.arena
	second, err := DecodeStateV3CoordinationArmBounded(secondRaw, &workspace)
	if err != nil {
		t.Fatalf("Decode second: %v", err)
	}
	if first.document.used != firstUsed || first.document.arena != firstArena {
		t.Fatal("workspace reuse mutated the first returned fixed-arena model")
	}
	if first.document.arena == second.document.arena {
		t.Fatal("distinct decoded documents unexpectedly share identical arena content")
	}
	first.Reset()
	if first != (StateV3BoundedCoordinationArm{}) {
		t.Fatal("model Reset did not zero the fixed arena and metadata")
	}
	workspace.Reset()
	if workspace != (StateV3BoundedWorkspace{}) {
		t.Fatal("workspace Reset did not zero parser scratch")
	}
}

func TestStateV3BoundedCompactModelFixedTopology(t *testing.T) {
	if got, want := unsafe.Sizeof(stateV3BoundedDocument{}), uintptr(17072); got != want {
		t.Fatalf("stateV3BoundedDocument size = %d, want %d", got, want)
	}
	for name, size := range map[string]uintptr{
		"lease":   unsafe.Sizeof(StateV3BoundedActiveLease{}),
		"claim":   unsafe.Sizeof(StateV3BoundedCoordinationClaim{}),
		"arm":     unsafe.Sizeof(StateV3BoundedCoordinationArm{}),
		"outcome": unsafe.Sizeof(StateV3BoundedCoordinationOutcome{}),
	} {
		if size != unsafe.Sizeof(stateV3BoundedDocument{}) {
			t.Fatalf("%s model size = %d, want %d", name, size, unsafe.Sizeof(stateV3BoundedDocument{}))
		}
	}
	assertFixedBoundedType(t, reflect.TypeOf(stateV3BoundedDocument{}))
	assertFixedBoundedType(t, reflect.TypeOf(StateV3BoundedWorkspace{}))
}

func TestStateV3BoundedExpectationFixedSingleArenaTopology(t *testing.T) {
	for name, value := range map[string]any{
		"acquire arm":     StateV3BoundedClaimAcquireArmExpectation{},
		"release arm":     StateV3BoundedClaimReleaseArmExpectation{},
		"acquire outcome": StateV3BoundedClaimAcquireOutcomeExpectation{},
		"release outcome": StateV3BoundedClaimReleaseOutcomeExpectation{},
	} {
		typ := reflect.TypeOf(value)
		arenas := 0
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.Type == reflect.TypeOf([stateV3BoundedCompactDocumentBytes]byte{}) {
				arenas++
			}
		}
		if arenas != 1 {
			t.Fatalf("%s expectation owns %d arenas, want 1", name, arenas)
		}
		assertFixedBoundedType(t, typ)
	}
}

func assertFixedBoundedType(t *testing.T, typ reflect.Type) {
	t.Helper()
	switch typ.Kind() {
	case reflect.Array:
		assertFixedBoundedType(t, typ.Elem())
	case reflect.Struct:
		for index := 0; index < typ.NumField(); index++ {
			assertFixedBoundedType(t, typ.Field(index).Type)
		}
	case reflect.Bool, reflect.Int32, reflect.Int64, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return
	default:
		t.Fatalf("bounded model contains non-fixed field type %s", typ)
	}
}

func TestStateV3BoundedCompactDecodersAllocateNothingAtMaximumCanonicalInput(t *testing.T) {
	for _, tc := range boundedMaximumInputs(t) {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.raw) != stateV3BoundedCompactDocumentBytes {
				t.Fatalf("maximum canonical input length = %d, want %d", len(tc.raw), stateV3BoundedCompactDocumentBytes)
			}
			var workspace StateV3BoundedWorkspace
			if allocations := testing.AllocsPerRun(100, func() {
				if err := tc.decode(tc.raw, &workspace); err != nil {
					t.Fatal(err)
				}
			}); allocations != 0 {
				t.Fatalf("maximum-input allocations = %v, want 0", allocations)
			}
		})
	}
}

type stateV3BoundedDecoderCase struct {
	name   string
	raw    []byte
	decode func([]byte, *StateV3BoundedWorkspace) error
}

func boundedMaximumInputs(t *testing.T) []stateV3BoundedDecoderCase {
	t.Helper()
	lease := StateV3ActiveOperationLease{SchemaVersion: "1", RepositoryID: "repo", RepositoryFullName: "org/repo", OriginalCommentID: "1", Command: "minor", RequestedVersion: "v1.2.0", ResolvedVersion: "v1.2.0", SourceRef: "refs/heads/main", SourceOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RequestKey: "", RequestSHA256: stateV3BoundedDigest, ReservationMarker: "marker", VersionResolutionSHA256: stateV3BoundedDigest, CoordinationRef: StateV3CoordinationRef, CoordinationClaimPath: "release-lines/1.2.json", CoordinationClaimOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CoordinationClaimBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CoordinationClaimSHA256: stateV3BoundedDigest, CoordinationParentOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	lease.RequestKey = strings.Repeat("x", stateV3BoundedCompactDocumentBytes-len(mustCanonicalStateV3(lease)))
	claim := StateV3ReleaseLineClaim{ReleaseLine: "1.2", RequestKey: "", RequestSHA256: stateV3BoundedDigest, LaneRef: StateV3MinorStateRef, LaneExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ReservationSHA256: stateV3BoundedDigest, VersionResolutionSHA256: stateV3BoundedDigest, Command: "minor", ResolvedVersion: "v1.2.0", State: "active", Phase: StateV3PhaseReserved, Attempt: 1}
	claim.RequestKey = strings.Repeat("x", stateV3BoundedCompactDocumentBytes-len(mustCanonicalStateV3(claim)))
	arm := StateV3CoordinationMutationArm{Operation: "claim_acquire", Ref: StateV3CoordinationRef, ClaimPath: "release-lines/1.2.json", RequestKey: "", ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IntendedClaimSHA256: stateV3BoundedDigest, Attempt: 1}
	arm.RequestKey = strings.Repeat("x", stateV3BoundedCompactDocumentBytes-len(mustCanonicalStateV3(arm)))
	outcome := StateV3CoordinationMutationOutcome{SchemaVersion: "1", Operation: "claim_acquire", StateRef: StateV3CoordinationRef, ArmPath: stateV3CoordinationArmPath, ArmBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmSHA256: stateV3BoundedDigest, ArmCommitOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmTreeOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ClaimPath: "release-lines/1.2.json", RequestKey: "", RequestSHA256: stateV3BoundedDigest, ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IntendedClaimSHA256: stateV3BoundedDigest, Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, ObservedRefOID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	outcome.RequestKey = strings.Repeat("x", stateV3BoundedCompactDocumentBytes-len(mustCanonicalStateV3(outcome)))
	return []stateV3BoundedDecoderCase{
		{"lease", mustCanonicalStateV3(lease), func(raw []byte, w *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3ActiveLeaseBounded(raw, w)
			return err
		}},
		{"claim", mustCanonicalStateV3(claim), func(raw []byte, w *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3CoordinationClaimBounded(raw, w)
			return err
		}},
		{"arm", mustCanonicalStateV3(arm), func(raw []byte, w *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3CoordinationArmBounded(raw, w)
			return err
		}},
		{"outcome", mustCanonicalStateV3(outcome), func(raw []byte, w *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3CoordinationOutcomeBounded(raw, w)
			return err
		}},
	}
}

func TestStateV3BoundedCompactStructuralDynamicDifferentials(t *testing.T) {
	// This is intentionally structural parity only: dynamic decode and
	// canonical re-encoding. Outcome lifecycle validity is deferred to the
	// existing observed-outcome validator and is not claimed by this parser.
	lease := StateV3ActiveOperationLease{SchemaVersion: "1", RepositoryID: "repo", RepositoryFullName: "org/repo", OriginalCommentID: "1", Command: "minor", RequestedVersion: "v1.2.0", ResolvedVersion: "v1.2.0", SourceRef: "refs/heads/main", SourceOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", RequestKey: "request", RequestSHA256: stateV3BoundedDigest, ReservationMarker: "marker", VersionResolutionSHA256: stateV3BoundedDigest, CoordinationRef: StateV3CoordinationRef, CoordinationClaimPath: "release-lines/1.2.json", CoordinationClaimOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CoordinationClaimBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CoordinationClaimSHA256: stateV3BoundedDigest, CoordinationParentOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	claim := StateV3ReleaseLineClaim{ReleaseLine: "1.2", RequestKey: "request", RequestSHA256: stateV3BoundedDigest, LaneRef: StateV3MinorStateRef, LaneExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ReservationSHA256: stateV3BoundedDigest, VersionResolutionSHA256: stateV3BoundedDigest, Command: "minor", ResolvedVersion: "v1.2.0", State: "active", Phase: StateV3PhaseReserved, Attempt: 1}
	arm := StateV3CoordinationMutationArm{Operation: "claim_acquire", Ref: StateV3CoordinationRef, ClaimPath: "release-lines/1.2.json", RequestKey: "request", ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IntendedClaimSHA256: stateV3BoundedDigest, Attempt: 1}
	outcome := StateV3CoordinationMutationOutcome{SchemaVersion: "1", Operation: "claim_acquire", StateRef: StateV3CoordinationRef, ArmPath: stateV3CoordinationArmPath, ArmBlobOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmSHA256: stateV3BoundedDigest, ArmCommitOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArmTreeOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ClaimPath: "release-lines/1.2.json", RequestKey: "request", RequestSHA256: stateV3BoundedDigest, ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IntendedClaimSHA256: stateV3BoundedDigest, Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, ObservedRefOID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	cases := []struct {
		name          string
		raw           []byte
		decodeDynamic func([]byte) bool
		decodeBounded func([]byte, *StateV3BoundedWorkspace) error
	}{
		{"lease", mustCanonicalStateV3(lease), func(raw []byte) bool {
			var value StateV3ActiveOperationLease
			return decodeStateV3Document(raw, MaxStateV3ActiveLeaseBytes, &value) == nil && bytes.Equal(mustCanonicalStateV3(value), raw)
		}, func(raw []byte, workspace *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3ActiveLeaseBounded(raw, workspace)
			return err
		}},
		{"claim", mustCanonicalStateV3(claim), func(raw []byte) bool {
			var value StateV3ReleaseLineClaim
			return decodeStateV3Document(raw, MaxStateV3CoordinationClaimBytes, &value) == nil && bytes.Equal(mustCanonicalStateV3(value), raw)
		}, func(raw []byte, workspace *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3CoordinationClaimBounded(raw, workspace)
			return err
		}},
		{"arm", mustCanonicalStateV3(arm), func(raw []byte) bool {
			var value StateV3CoordinationMutationArm
			return decodeStateV3Document(raw, MaxStateV3CoordinationArmBytes, &value) == nil && bytes.Equal(mustCanonicalStateV3(value), raw)
		}, func(raw []byte, workspace *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3CoordinationArmBounded(raw, workspace)
			return err
		}},
		{"outcome", mustCanonicalStateV3(outcome), func(raw []byte) bool {
			var value StateV3CoordinationMutationOutcome
			return decodeStateV3Document(raw, MaxStateV3CoordinationOutcomeBytes, &value) == nil && bytes.Equal(mustCanonicalStateV3(value), raw)
		}, func(raw []byte, workspace *StateV3BoundedWorkspace) error {
			_, err := DecodeStateV3CoordinationOutcomeBounded(raw, workspace)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var workspace StateV3BoundedWorkspace
			if !tc.decodeDynamic(tc.raw) {
				t.Fatal("dynamic structural decoder/canonical encoder rejected fixture")
			}
			if err := tc.decodeBounded(tc.raw, &workspace); err != nil {
				t.Fatalf("bounded structural decoder rejected fixture: %v", err)
			}
			for _, mutation := range [][]byte{append([]byte(" "), tc.raw...), append(append([]byte(nil), tc.raw...), ' '), bytes.Replace(tc.raw, []byte(`:`), []byte(` :`), 1)} {
				if tc.decodeDynamic(mutation) || tc.decodeBounded(mutation, &workspace) == nil {
					t.Fatalf("structural canonical differential accepted mutation %q", mutation)
				}
			}
		})
	}
}

func boundedCanonicalArm(t *testing.T, requestKey string) []byte {
	t.Helper()
	return mustCanonicalStateV3(StateV3CoordinationMutationArm{Operation: "claim_acquire", Ref: StateV3CoordinationRef, ClaimPath: "release-lines/1.2.json", RequestKey: requestKey, ReleaseLine: "1.2", LaneRef: StateV3MinorStateRef, ResolvedVersion: "v1.2.0", ExpectedHeadOID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IntendedClaimSHA256: stateV3BoundedDigest, Attempt: 1})
}
