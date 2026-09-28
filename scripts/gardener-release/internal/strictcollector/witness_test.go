// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"reflect"
	"testing"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

func TestStateV3FullWitnessesAreFixedRawFreeAndRoleBound(t *testing.T) {
	assertFixedCompactType(t, reflect.TypeFor[stateV3FullWitnessEntry]())
	assertFixedCompactType(t, reflect.TypeFor[stateV3FullLaneSnapshotWitness]())
	assertFixedCompactType(t, reflect.TypeFor[stateV3FullCoordinationSnapshotWitness]())
	assertFixedCompactType(t, reflect.TypeFor[stateV3FullLaneWitnessHistory]())
	assertFixedCompactType(t, reflect.TypeFor[stateV3FullCoordinationWitnessHistory]())

	var lane stateV3FullLaneWitnessHistory
	var coordination stateV3FullCoordinationWitnessHistory
	if got, want := len(lane.snapshots), gardenerrelease.MaxStateV3StateLaneHistoryCommits; got != want {
		t.Fatalf("lane witness capacity=%d want=%d", got, want)
	}
	if got, want := len(lane.snapshots[0].entries), 5; got != want {
		t.Fatalf("lane leaf capacity=%d want=%d", got, want)
	}
	if got, want := len(coordination.snapshots), gardenerrelease.MaxStateV3CoordinationHistoryCommits; got != want {
		t.Fatalf("coordination witness capacity=%d want=%d", got, want)
	}
	if got, want := len(coordination.snapshots[0].entries), gardenerrelease.MaxStateV3CoordinationReleaseProofs+1; got != want {
		t.Fatalf("coordination leaf capacity=%d want=%d", got, want)
	}
}

func TestStateV3FullLaneWitnessCapturesOnlyCompleteApprovedLeafSet(t *testing.T) {
	commit, tree, parent := compactAdmissionOID(91_000), compactAdmissionOID(91_001), compactAdmissionOID(91_002)
	entries := []wireTreeEntry{
		witnessTreeEntry(gardenerrelease.StateV3ActiveLeasePath, compactAdmissionOID(91_010)),
		witnessTreeEntry("requests/1/2/state.json", compactAdmissionOID(91_011)),
		witnessTreeEntry("requests/1/2/reservation.json", compactAdmissionOID(91_012)),
		witnessTreeEntry("requests/1/2/prepared.json", compactAdmissionOID(91_013)),
		witnessTreeEntry("requests/1/2/generation.bundle", compactAdmissionOID(91_014)),
		{Path: "requests", Mode: "040000", Type: "tree", SHA: compactAdmissionOID(91_015)},
	}
	raw := wireRawCommit{SHA: commit, Tree: wireTreeRef{SHA: tree}, Parents: []wireParent{{SHA: parent}}}
	value := wireTree{SHA: tree, Tree: entries}
	var witness stateV3FullLaneWitnessHistory
	if !witness.appendWitness(raw, value, stateV3AssemblyMinor) {
		t.Fatal("could not issue witness from complete approved lane tree")
	}
	snapshot := witness.snapshots[0]
	if snapshot.commitOID != mustFixedOID(commit) || snapshot.treeOID != mustFixedOID(tree) || snapshot.parentOID != mustFixedOID(parent) || snapshot.ordinal != 0 || snapshot.role != stateV3AssemblyMinor || snapshot.verification != stateV3WitnessVerified || snapshot.treeEmpty || snapshot.count != 5 {
		t.Fatalf("witness=%#v", snapshot)
	}
	if got := string(snapshot.entries[0].path[:snapshot.entries[0].len]); got != gardenerrelease.StateV3ActiveLeasePath {
		t.Fatalf("first sorted witness path=%q", got)
	}
}

func TestStateV3FullWitnessRejectsUnapprovedRegularLeaf(t *testing.T) {
	commit, tree := compactAdmissionOID(92_000), compactAdmissionOID(92_001)
	raw := wireRawCommit{SHA: commit, Tree: wireTreeRef{SHA: tree}}
	value := wireTree{SHA: tree, Tree: []wireTreeEntry{witnessTreeEntry("unexpected.json", compactAdmissionOID(92_002))}}
	var witness stateV3FullLaneWitnessHistory
	if witness.appendWitness(raw, value, stateV3AssemblyMinor) || witness.count != 0 {
		t.Fatal("unapproved regular leaf issued witness")
	}
}

func witnessTreeEntry(path, oid string) wireTreeEntry {
	size := int64(1)
	return wireTreeEntry{Path: path, Mode: "100644", Type: "blob", SHA: oid, Size: &size}
}
