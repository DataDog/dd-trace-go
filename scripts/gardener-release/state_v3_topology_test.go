// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"testing"
)

func TestStateV3PolicyHistoryTopologyBounds(t *testing.T) {
	minimumMinor := 4*MaxStateV3Tags + StateV3PrepareHistoryOverhead
	minimumPatch := 4*MaxStateV3Tags + StateV3OtherHistoryOverhead
	for name, mutate := range map[string]func(*StateV3Policy){
		"minor minimum":        func(policy *StateV3Policy) { policy.StateLanes.Minor.MaxHistoryCommits = minimumMinor },
		"patch minimum":        func(policy *StateV3Policy) { policy.StateLanes.Patch.MaxHistoryCommits = minimumPatch },
		"coordination minimum": func(policy *StateV3Policy) { policy.Coordination.MaxHistoryCommits = 2 },
		"all maximum": func(policy *StateV3Policy) {
			policy.StateLanes.Minor.MaxHistoryCommits = MaxStateV3StateLaneHistoryCommits
			policy.StateLanes.Patch.MaxHistoryCommits = MaxStateV3StateLaneHistoryCommits
			policy.Coordination.MaxHistoryCommits = MaxStateV3CoordinationHistoryCommits
		},
	} {
		t.Run(name, func(t *testing.T) {
			policy := fixtureStateV3Policy()
			mutate(&policy)
			if err := ValidateStateV3Policy(policy); err != nil {
				t.Fatalf("valid bounded policy rejected: %v", err)
			}
		})
	}
	for name, mutate := range map[string]func(*StateV3Policy){
		"minor below minimum":        func(policy *StateV3Policy) { policy.StateLanes.Minor.MaxHistoryCommits = minimumMinor - 1 },
		"patch below minimum":        func(policy *StateV3Policy) { policy.StateLanes.Patch.MaxHistoryCommits = minimumPatch - 1 },
		"coordination below minimum": func(policy *StateV3Policy) { policy.Coordination.MaxHistoryCommits = 1 },
		"minor old maximum":          func(policy *StateV3Policy) { policy.StateLanes.Minor.MaxHistoryCommits = MaxStateV3HistoryCommits },
		"minor one above": func(policy *StateV3Policy) {
			policy.StateLanes.Minor.MaxHistoryCommits = MaxStateV3StateLaneHistoryCommits + 1
		},
		"patch one above": func(policy *StateV3Policy) {
			policy.StateLanes.Patch.MaxHistoryCommits = MaxStateV3StateLaneHistoryCommits + 1
		},
		"coordination one above": func(policy *StateV3Policy) {
			policy.Coordination.MaxHistoryCommits = MaxStateV3CoordinationHistoryCommits + 1
		},
	} {
		t.Run(name, func(t *testing.T) {
			policy := fixtureStateV3Policy()
			mutate(&policy)
			if err := ValidateStateV3Policy(policy); err == nil {
				t.Fatal("out-of-topology policy accepted")
			}
		})
	}
}

func TestStateV3ThreeSessionReadTopology(t *testing.T) {
	if MaxStateV3StateLaneHistoryCommits != 455 {
		t.Fatalf("state history maximum = %d, want 455", MaxStateV3StateLaneHistoryCommits)
	}
	if MaxStateV3CoordinationHistoryCommits != 512 {
		t.Fatalf("coordination history maximum = %d, want 512", MaxStateV3CoordinationHistoryCommits)
	}
	if stateV3StateSpineMaximumReads != 4091 {
		t.Fatalf("state spine reads = %d, want 4091", stateV3StateSpineMaximumReads)
	}
	if stateV3CoordinationSpineMaximumReads != 3582 {
		t.Fatalf("coordination spine reads = %d, want 3582", stateV3CoordinationSpineMaximumReads)
	}
	if stateV3TopologyMaximumLogicalReads != 11764 {
		t.Fatalf("three-session logical reads = %d, want 11764", stateV3TopologyMaximumLogicalReads)
	}
	if stateV3TopologyMaximumAttempts != 47056 {
		t.Fatalf("three-session attempts = %d, want 47056", stateV3TopologyMaximumAttempts)
	}
}

func TestStateV3TerminalCleanupIsExactAndLeavesEmptyTree(t *testing.T) {
	policy := fixtureStateV3Policy()
	record := fixtureStateV3Record(t)
	authentication := fixtureStateV3Authentication(t, policy, record)
	if len(authentication.Current.Tree.Entries) != 0 || authentication.Current.LeasePresent || authentication.Current.RecordPresent {
		t.Fatal("complete fixture did not end at the required empty terminal cleanup tree")
	}
	if len(authentication.Current.Commit.ChangedPaths) != stateV3TerminalCleanupPathCount {
		t.Fatalf("cleanup changes = %d, want %d", len(authentication.Current.Commit.ChangedPaths), stateV3TerminalCleanupPathCount)
	}
	raw, err := canonicalJSON(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateStateV3Record(raw, record, policy, authentication); err != nil {
		t.Fatalf("exact terminal cleanup rejected: %v", err)
	}

	for name, mutate := range map[string]func(*StateV3Authentication){
		"cleanup retains file": func(authentication *StateV3Authentication) {
			authentication.Current.Tree.Entries = []StateV3StateTreeEntry{{Path: StateV3ActiveLeasePath, Mode: "100644", Type: "blob", OID: v3OIDa}}
		},
		"cleanup omits deletion": func(authentication *StateV3Authentication) {
			authentication.Current.Commit.ChangedPaths = authentication.Current.Commit.ChangedPaths[:stateV3TerminalCleanupPathCount-1]
		},
		"cleanup adds file": func(authentication *StateV3Authentication) {
			authentication.Current.Commit.ChangedPaths[0].ChildOID = v3OIDa
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneStateV3(t, authentication)
			mutate(&candidate)
			if err := ValidateStateV3Record(raw, record, policy, candidate); err == nil {
				t.Fatal("non-empty or non-exact terminal cleanup accepted")
			}
		})
	}
}

func TestStateV3CoordinationClaimsAreBoundedToDistinctLanes(t *testing.T) {
	minor := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	patch := fixtureStateV3PatchOperation(t, "v2.10", "v2.10.1")
	fixtureStateV3SetCoordinationClaims(t, &minor.Reservation.VersionResolution, []StateV3ReleaseLineClaimEvidence{minor.Reservation.CoordinationClaim, patch.Reservation.CoordinationClaim})
	current := minor.Reservation.VersionResolution.Coordination.Authentication.Current
	if claims, ok := stateV3CoordinationClaims(current); !ok || len(claims) != MaxStateV3CoordinationReleaseProofs {
		t.Fatal("two distinct fixed-lane active claims rejected")
	}
	third := fixtureStateV3PatchOperation(t, "v3.10", "v3.10.1")
	fixtureStateV3SetCoordinationClaims(t, &minor.Reservation.VersionResolution, []StateV3ReleaseLineClaimEvidence{minor.Reservation.CoordinationClaim, patch.Reservation.CoordinationClaim, third.Reservation.CoordinationClaim})
	if _, ok := stateV3CoordinationClaims(minor.Reservation.VersionResolution.Coordination.Authentication.Current); ok {
		t.Fatal("third or same-lane active claim accepted")
	}
}

func TestStateV3TypedDocumentBounds(t *testing.T) {
	record := fixtureStateV3Record(t)
	raw, err := canonicalJSON(record)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > MaxStateV3StateRecordBytes {
		t.Fatalf("valid maximal fixture has %d bytes, record limit is %d", len(raw), MaxStateV3StateRecordBytes)
	}
	oversized := append(bytes.Repeat([]byte{' '}, MaxStateV3StateRecordBytes-len(raw)+1), raw...)
	if _, err := DecodeStateV3Record(oversized); err == nil {
		t.Fatal("record one byte above typed limit accepted")
	}

	lease := stateV3LeaseForReservation(record.Reservation)
	leaseRaw, err := canonicalJSON(lease)
	if err != nil {
		t.Fatal(err)
	}
	if len(leaseRaw) > MaxStateV3ActiveLeaseBytes {
		t.Fatalf("valid lease has %d bytes, lease limit is %d", len(leaseRaw), MaxStateV3ActiveLeaseBytes)
	}
	snapshot := fixtureStateV3Authentication(t, fixtureStateV3Policy(), fixtureStateV3RecordAtEventCount(t, record, 1)).Predecessors[0]
	snapshot.RawLease = append(bytes.Repeat([]byte{' '}, MaxStateV3ActiveLeaseBytes-len(leaseRaw)+1), leaseRaw...)
	if _, ok := stateV3SnapshotLease(snapshot); ok {
		t.Fatal("lease one byte above typed limit accepted")
	}

	if record.Prepared == nil {
		t.Fatal("prepared record required")
	}
	manifest := stateV3PreparedManifest{SchemaVersion: StateV3SchemaVersion, Bundle: record.Prepared.Bundle, AdditionCount: record.Prepared.AdditionCount, TotalDecodedAdditionBytes: record.Prepared.TotalDecodedAdditionBytes, ToolDigest: record.Prepared.ToolDigest, ValidatorDigest: record.Prepared.ValidatorDigest, Mutation: record.Prepared.Mutation, TagPlans: record.TagPlans}
	manifestRaw, err := canonicalJSON(manifest)
	if err != nil {
		t.Fatal(err)
	}
	assertStateV3DocumentOverLimit(t, "prepared manifest", manifestRaw, MaxStateV3PreparedManifestBytes, &stateV3PreparedManifest{})

	reservationRaw, err := canonicalJSON(record.Reservation)
	if err != nil {
		t.Fatal(err)
	}
	assertStateV3DocumentOverLimit(t, "reservation", reservationRaw, MaxStateV3ReservationBytes, &StateV3Reservation{})

	coordination := record.Reservation.VersionResolution.Coordination.Authentication.Current
	if coordination.Arm == nil {
		t.Fatal("coordination arm required")
	}
	armRaw, err := canonicalJSON(coordination.Arm.Arm)
	if err != nil {
		t.Fatal(err)
	}
	assertStateV3DocumentOverLimit(t, "coordination arm", armRaw, MaxStateV3CoordinationArmBytes, &StateV3CoordinationMutationArm{})

	claimRaw, err := canonicalJSON(record.Reservation.CoordinationClaim.Claim)
	if err != nil {
		t.Fatal(err)
	}
	assertStateV3DocumentOverLimit(t, "coordination claim", claimRaw, MaxStateV3CoordinationClaimBytes, &StateV3ReleaseLineClaim{})
}

func TestStateV3DocumentStoreTopology(t *testing.T) {
	if stateV3MinimumCompletedRecordRevisions != 15 || stateV3CompletedOperationSnapshots != 18 || MaxStateV3LaneOperationWindows != 26 || stateV3ByteMaxStateRecordVersionsPerLane != 377 {
		t.Fatalf("unexpected state lifecycle topology: revisions=%d snapshots=%d windows=%d records=%d", stateV3MinimumCompletedRecordRevisions, stateV3CompletedOperationSnapshots, MaxStateV3LaneOperationWindows, stateV3ByteMaxStateRecordVersionsPerLane)
	}
	if MaxStateV3DocumentBlobVersions != 2495 || MaxStateV3DocumentStoreRawBytes != 186_138_624 || MaxStateV3DocumentProvenanceBindings != 6035 {
		t.Fatalf("unexpected document-store topology: blobs=%d bytes=%d bindings=%d", MaxStateV3DocumentBlobVersions, MaxStateV3DocumentStoreRawBytes, MaxStateV3DocumentProvenanceBindings)
	}
}

func assertStateV3DocumentOverLimit(t *testing.T, name string, raw []byte, maximum int, target any) {
	t.Helper()
	if len(raw) > maximum {
		t.Fatalf("valid %s has %d bytes, limit is %d", name, len(raw), maximum)
	}
	oversized := append(bytes.Repeat([]byte{' '}, maximum-len(raw)+1), raw...)
	if err := decodeStateV3Document(oversized, maximum, target); err == nil {
		t.Fatalf("%s one byte above typed limit accepted", name)
	}
}
