// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
)

func TestStateV3AuthenticatesPhysicalPreparedSequenceAndExactTreeDeltas(t *testing.T) {
	full := fixtureStateV3Record(t)
	prepared := fixtureStateV3RecordAtEventCount(t, full, 2)
	policy := fixtureStateV3Policy()
	authentication := fixtureStateV3Authentication(t, policy, prepared)
	raw, _ := canonicalJSON(prepared)
	if err := ValidateStateV3Record(raw, prepared, policy, authentication); err != nil {
		t.Fatalf("physical reserved, staged-envelope, prepared sequence rejected: %v", err)
	}
	if len(authentication.Current.Commit.ChangedPaths) != 1 || authentication.Current.Commit.ChangedPaths[0].Path != authentication.Current.RecordPath {
		t.Fatal("prepared phase did not use a separate one-file state transition")
	}
	staged := authentication.Predecessors[0]
	if staged.StagedEnvelope == nil || len(staged.Commit.ChangedPaths) != StateV3PreparedAdditionCount || !bytes.Equal(staged.RawRecord, authentication.Predecessors[1].RawRecord) {
		t.Fatal("staged envelope was not an exact three-addition commit over an unchanged reserved record")
	}
	var reserved StateV3Record
	if err := json.Unmarshal(staged.RawRecord, &reserved); err != nil {
		t.Fatal(err)
	}
	lane, _ := stateV3LaneForReservation(reserved.Reservation, policy)
	stagedCoordination := prepared.Reservation.VersionResolution.Coordination.Authentication
	stagedAuthentication := StateV3Authentication{StateRef: lane.StateRef, HeadOID: staged.Commit.OID, CheckpointOID: lane.CheckpointOID, Current: staged, Predecessors: authentication.Predecessors[1:], Coordination: &stagedCoordination}
	if err := ValidateStateV3Record(staged.RawRecord, reserved, policy, stagedAuthentication); err != nil {
		t.Fatalf("authenticated staged-envelope intermediate rejected: %v", err)
	}
	for name, mutate := range map[string]func(*StateV3Authentication){
		"omitted delta": func(a *StateV3Authentication) {
			a.Predecessors[0].Commit.ChangedPaths = a.Predecessors[0].Commit.ChangedPaths[:2]
		},
		"extra delta": func(a *StateV3Authentication) {
			a.Current.Commit.ChangedPaths = append(a.Current.Commit.ChangedPaths, StateV3ChangedPath{Path: "requests/123/999/state.json", ChildOID: v3OIDb})
		},
		"wrong delta oid": func(a *StateV3Authentication) { a.Predecessors[0].Commit.ChangedPaths[0].ChildOID = v3OIDd },
		"impossible four-path preparation": func(a *StateV3Authentication) {
			a.Predecessors[0].Commit.ChangedPaths = append(a.Predecessors[0].Commit.ChangedPaths, StateV3ChangedPath{Path: "requests/123/789/state.json", ParentOID: a.Predecessors[1].RecordBlobOID, ChildOID: a.Predecessors[0].RecordBlobOID})
			sort.Slice(a.Predecessors[0].Commit.ChangedPaths, func(i, j int) bool {
				return a.Predecessors[0].Commit.ChangedPaths[i].Path < a.Predecessors[0].Commit.ChangedPaths[j].Path
			})
		},
		"hidden tree addition": func(a *StateV3Authentication) {
			a.Predecessors[0].Tree.Entries = append(a.Predecessors[0].Tree.Entries, StateV3StateTreeEntry{Path: "requests/123/999/state.json", Mode: "100644", Type: "blob", OID: v3OIDd})
			sort.Slice(a.Predecessors[0].Tree.Entries, func(i, j int) bool {
				return a.Predecessors[0].Tree.Entries[i].Path < a.Predecessors[0].Tree.Entries[j].Path
			})
		},
		"altered staged bytes": func(a *StateV3Authentication) {
			a.Predecessors[0].StagedEnvelope.Files[1].Raw = append(a.Predecessors[0].StagedEnvelope.Files[1].Raw, ' ')
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneStateV3(t, authentication)
			mutate(&candidate)
			if err := ValidateStateV3Record(raw, prepared, policy, candidate); err == nil {
				t.Fatal("invalid physical state sequence accepted")
			}
		})
	}
}

func TestStateV3RequiresOneDurableEventPerRecordCommitAndRejectsActiveInterleaving(t *testing.T) {
	record := fixtureStateV3Record(t)
	policy := fixtureStateV3Policy()
	raw, _ := canonicalJSON(record)
	base := fixtureStateV3Authentication(t, policy, record)

	multi := cloneStateV3(t, base)
	multi.Predecessors = append(multi.Predecessors[:1], multi.Predecessors[2:]...)
	multi.Current.Commit.ParentOID = multi.Predecessors[0].Commit.OID
	multi.Current.Commit.ChangedPaths = stateV3TreeChanges(multi.Predecessors[0].Tree, multi.Current.Tree)
	if err := ValidateStateV3Record(raw, record, policy, multi); err == nil {
		t.Fatal("state commit appending multiple operation events accepted")
	}

	direct := cloneStateV3(t, base)
	stagedIndex := -1
	for index := range direct.Predecessors {
		if direct.Predecessors[index].StagedEnvelope != nil {
			var predecessorRecord StateV3Record
			if json.Unmarshal(direct.Predecessors[index].RawRecord, &predecessorRecord) == nil && len(predecessorRecord.Events) == 1 {
				stagedIndex = index
				break
			}
		}
	}
	if stagedIndex < 0 {
		t.Fatal("missing staged reserved fixture")
	}
	direct.Predecessors = direct.Predecessors[stagedIndex:]
	direct.Current.Commit.ParentOID = direct.Predecessors[0].Commit.OID
	direct.Current.Commit.ChangedPaths = stateV3TreeChanges(direct.Predecessors[0].Tree, direct.Current.Tree)
	if err := ValidateStateV3Record(raw, record, policy, direct); err == nil {
		t.Fatal("direct reserved-to-complete durable phase skip accepted")
	}

	mixed := cloneStateV3(t, base)
	mixed.Current.Tree.Entries = append(mixed.Current.Tree.Entries, StateV3StateTreeEntry{Path: "requests/123/999/state.json", Mode: "100644", Type: "blob", OID: v3OIDd})
	sort.Slice(mixed.Current.Tree.Entries, func(i, j int) bool { return mixed.Current.Tree.Entries[i].Path < mixed.Current.Tree.Entries[j].Path })
	mixed.Current.Commit.ChangedPaths = stateV3TreeChanges(mixed.Predecessors[0].Tree, mixed.Current.Tree)
	if err := ValidateStateV3Record(raw, record, policy, mixed); err == nil {
		t.Fatal("commit changing this operation and another operation accepted")
	}

	interleaved := cloneStateV3(t, base)
	previous := interleaved.Current
	current := cloneStateV3(t, previous)
	current.Commit.OID = fmt.Sprintf("%040x", 999001)
	current.Commit.ParentOID = previous.Commit.OID
	current.Commit.TreeOID = fmt.Sprintf("%040x", 999002)
	current.Tree.OID = current.Commit.TreeOID
	current.Tree.Entries = append(current.Tree.Entries, StateV3StateTreeEntry{Path: "requests/123/999/state.json", Mode: "100644", Type: "blob", OID: v3OIDd})
	sort.Slice(current.Tree.Entries, func(i, j int) bool { return current.Tree.Entries[i].Path < current.Tree.Entries[j].Path })
	current.Commit.ChangedPaths = stateV3TreeChanges(previous.Tree, current.Tree)
	interleaved.Current = current
	interleaved.HeadOID = current.Commit.OID
	interleaved.Predecessors = append([]StateV3StateSnapshot{previous}, interleaved.Predecessors...)
	if err := ValidateStateV3Record(raw, record, policy, interleaved); err == nil {
		t.Fatal("byte-identical unrelated-operation commit accepted while global lease was held")
	}
}
