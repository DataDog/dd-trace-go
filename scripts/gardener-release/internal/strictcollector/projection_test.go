// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"testing"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

func TestSealedProjectionInputsRequireExactWitnessStoreBinding(t *testing.T) {
	store := &stateV3DocumentStore{}
	raw := []byte("bundle")
	key := documentKey(stateV3DocumentBundle, raw, 'a')
	entry := documentEntry(stateV3DocumentBundle, "requests/1/2/generation.bundle", stateV3AssemblyMinor, 'a')
	if !store.putEntry(key, entry, raw) {
		t.Fatal("put entry")
	}
	commitOID := mustFixedOID(entry.commitOID)
	treeOID := mustFixedOID(entry.treeOID)
	blobOID := mustFixedOID(entry.oid)
	var path [stateV3StoredPathBytes]byte
	copy(path[:], entry.path)
	witnessEntry := stateV3FullWitnessEntry{oid: blobOID, kind: stateV3DocumentBundle, path: path, len: uint8(len(entry.path))}
	minorWitness := stateV3FullLaneSnapshotWitness{
		commitOID: commitOID, treeOID: treeOID, ordinal: 0, role: stateV3AssemblyMinor, verification: stateV3WitnessVerified,
		entries: [5]stateV3FullWitnessEntry{witnessEntry}, count: 1,
	}
	minorCompact := stateV3CompactSnapshot{commitOID: commitOID, treeOID: treeOID, ordinal: 0, entries: [5]stateV3CompactDocumentEntry{{oid: blobOID, kind: stateV3DocumentBundle, path: path, len: uint8(len(entry.path))}}, count: 1}
	emptyLane := func(role stateV3AssemblyRole) stateV3SealedLaneHistory {
		witness := stateV3FullLaneSnapshotWitness{ordinal: 0, role: role, verification: stateV3WitnessVerified, treeEmpty: true}
		return stateV3SealedLaneHistory{snapshots: [gardenerrelease.MaxStateV3StateLaneHistoryCommits]stateV3CompactSnapshot{{ordinal: 0, treeEmpty: true}}, witnesses: [gardenerrelease.MaxStateV3StateLaneHistoryCommits]stateV3FullLaneSnapshotWitness{witness}, count: 1, role: role, sealed: true}
	}
	coordWitness := stateV3FullCoordinationSnapshotWitness{ordinal: 0, role: stateV3AssemblyCoordination, verification: stateV3WitnessVerified, treeEmpty: true}
	op := &stateV3AssemblyOperation{
		store:  store,
		policy: &stateV3AssemblyPolicy{},
		seals: stateV3SealedSpines{
			minor:        stateV3SealedLaneHistory{snapshots: [gardenerrelease.MaxStateV3StateLaneHistoryCommits]stateV3CompactSnapshot{minorCompact}, witnesses: [gardenerrelease.MaxStateV3StateLaneHistoryCommits]stateV3FullLaneSnapshotWitness{minorWitness}, count: 1, role: stateV3AssemblyMinor, sealed: true},
			patch:        emptyLane(stateV3AssemblyPatch),
			coordination: stateV3SealedCoordinationHistory{snapshots: [gardenerrelease.MaxStateV3CoordinationHistoryCommits]stateV3CompactSnapshot{{ordinal: 0, treeEmpty: true}}, witnesses: [gardenerrelease.MaxStateV3CoordinationHistoryCommits]stateV3FullCoordinationSnapshotWitness{coordWitness}, count: 1, role: stateV3AssemblyCoordination, sealed: true},
		},
	}
	if !op.verifySealedProjectionInputs() {
		t.Fatal("exact witness binding rejected")
	}
	store.bindings[0].treeOID = [20]byte{}
	if op.verifySealedProjectionInputs() {
		t.Fatal("mismatched witness binding accepted")
	}
}

func TestProjectionWitnessTopologyRejectsUnsortedOrInconsistentLeaves(t *testing.T) {
	witness := stateV3FullLaneSnapshotWitness{role: stateV3AssemblyMinor, verification: stateV3WitnessVerified, count: 2}
	copy(witness.entries[0].path[:], "z")
	witness.entries[0].len = 1
	copy(witness.entries[1].path[:], "a")
	witness.entries[1].len = 1
	if projectionLaneWitnessTopologyValid(witness, stateV3AssemblyMinor) {
		t.Fatal("unsorted witness accepted")
	}
	witness.count = 0
	witness.treeEmpty = false
	if projectionLaneWitnessTopologyValid(witness, stateV3AssemblyMinor) {
		t.Fatal("inconsistent empty witness accepted")
	}
}
