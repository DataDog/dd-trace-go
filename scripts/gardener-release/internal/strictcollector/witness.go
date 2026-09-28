// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"

// appendWitness is the sole issuer for a lane snapshot's modeled-full-leaf
// witness. Its caller has already accepted stateV3SnapshotEvidence for the
// same raw/tree pair; this function independently requires complete
// role-specific document discovery before copying every accepted regular leaf.
func (h *stateV3FullLaneWitnessHistory) appendWitness(raw wireRawCommit, tree wireTree, role stateV3AssemblyRole) bool {
	if h == nil || h.count == gardenerrelease.MaxStateV3StateLaneHistoryCommits || role != stateV3AssemblyMinor && role != stateV3AssemblyPatch {
		return false
	}
	commitOID, commitOK := decodeFixedOID(raw.SHA)
	treeOID, treeOK := decodeFixedOID(tree.SHA)
	if !commitOK || !treeOK || raw.Tree.SHA != tree.SHA || len(raw.Parents) > 1 {
		return false
	}
	var parentOID [20]byte
	if len(raw.Parents) == 1 {
		var parentOK bool
		parentOID, parentOK = decodeFixedOID(raw.Parents[0].SHA)
		if !parentOK {
			return false
		}
	}
	entries, ok := stateV3DiscoverDocumentEntries(role, tree.Tree)
	if !ok || len(entries) > len(h.snapshots[0].entries) {
		return false
	}
	snapshot := &h.snapshots[h.count]
	snapshot.commitOID, snapshot.treeOID, snapshot.parentOID, snapshot.ordinal = commitOID, treeOID, parentOID, h.count
	snapshot.role, snapshot.verification, snapshot.treeEmpty = role, stateV3WitnessVerified, len(tree.Tree) == 0
	for i := range entries {
		entry := entries[i]
		oid, oidOK := decodeFixedOID(entry.oid)
		if !oidOK || len(entry.path) > stateV3StoredPathBytes {
			return false
		}
		snapshot.entries[i] = stateV3FullWitnessEntry{oid: oid, kind: entry.kind, len: uint8(len(entry.path))}
		copy(snapshot.entries[i].path[:], entry.path)
	}
	snapshot.count = uint8(len(entries))
	h.count++
	return true
}

// appendWitness is the sole issuer for a coordination snapshot's
// modeled-full-leaf witness. Coordination has a distinct three-leaf capacity:
// two claims plus one mutually exclusive arm or outcome document.
func (h *stateV3FullCoordinationWitnessHistory) appendWitness(raw wireRawCommit, tree wireTree) bool {
	if h == nil || h.count == gardenerrelease.MaxStateV3CoordinationHistoryCommits {
		return false
	}
	commitOID, commitOK := decodeFixedOID(raw.SHA)
	treeOID, treeOK := decodeFixedOID(tree.SHA)
	if !commitOK || !treeOK || raw.Tree.SHA != tree.SHA || len(raw.Parents) > 1 {
		return false
	}
	var parentOID [20]byte
	if len(raw.Parents) == 1 {
		var parentOK bool
		parentOID, parentOK = decodeFixedOID(raw.Parents[0].SHA)
		if !parentOK {
			return false
		}
	}
	entries, ok := stateV3DiscoverDocumentEntries(stateV3AssemblyCoordination, tree.Tree)
	if !ok || len(entries) > len(h.snapshots[0].entries) {
		return false
	}
	snapshot := &h.snapshots[h.count]
	snapshot.commitOID, snapshot.treeOID, snapshot.parentOID, snapshot.ordinal = commitOID, treeOID, parentOID, h.count
	snapshot.role, snapshot.verification, snapshot.treeEmpty = stateV3AssemblyCoordination, stateV3WitnessVerified, len(tree.Tree) == 0
	for i := range entries {
		entry := entries[i]
		oid, oidOK := decodeFixedOID(entry.oid)
		if !oidOK || len(entry.path) > stateV3StoredPathBytes {
			return false
		}
		snapshot.entries[i] = stateV3FullWitnessEntry{oid: oid, kind: entry.kind, len: uint8(len(entry.path))}
		copy(snapshot.entries[i].path[:], entry.path)
	}
	snapshot.count = uint8(len(entries))
	h.count++
	return true
}

func laneWitnessMatchesCompact(witness stateV3FullLaneSnapshotWitness, compact stateV3CompactSnapshot, role stateV3AssemblyRole) bool {
	if witness.commitOID != compact.commitOID || witness.treeOID != compact.treeOID || witness.parentOID != compact.parentOID || witness.ordinal != compact.ordinal || witness.role != role || witness.verification != stateV3WitnessVerified || witness.count != compact.count || witness.treeEmpty != compact.treeEmpty {
		return false
	}
	for i := 0; i < int(compact.count); i++ {
		if witness.entries[i].oid != compact.entries[i].oid || witness.entries[i].kind != compact.entries[i].kind || witness.entries[i].path != compact.entries[i].path || witness.entries[i].len != compact.entries[i].len {
			return false
		}
	}
	return true
}

func sealedLaneWitnessesMatch(history stateV3SealedLaneHistory, role stateV3AssemblyRole, maximum int) bool {
	if !history.sealed || history.role != role || history.count == 0 || int(history.count) > maximum {
		return false
	}
	for ordinal := 0; ordinal < int(history.count); ordinal++ {
		if !laneWitnessMatchesCompact(history.witnesses[ordinal], history.snapshots[ordinal], role) {
			return false
		}
	}
	return true
}

func sealedCoordinationWitnessesMatch(history stateV3SealedCoordinationHistory, maximum int) bool {
	if !history.sealed || history.role != stateV3AssemblyCoordination || history.count == 0 || int(history.count) > maximum {
		return false
	}
	for ordinal := 0; ordinal < int(history.count); ordinal++ {
		if !coordinationWitnessMatchesCompact(history.witnesses[ordinal], history.snapshots[ordinal]) {
			return false
		}
	}
	return true
}

func coordinationWitnessMatchesCompact(witness stateV3FullCoordinationSnapshotWitness, compact stateV3CompactSnapshot) bool {
	if witness.commitOID != compact.commitOID || witness.treeOID != compact.treeOID || witness.parentOID != compact.parentOID || witness.ordinal != compact.ordinal || witness.role != stateV3AssemblyCoordination || witness.verification != stateV3WitnessVerified || witness.count != compact.count || witness.treeEmpty != compact.treeEmpty {
		return false
	}
	for i := 0; i < int(compact.count); i++ {
		if witness.entries[i].oid != compact.entries[i].oid || witness.entries[i].kind != compact.entries[i].kind || witness.entries[i].path != compact.entries[i].path || witness.entries[i].len != compact.entries[i].len {
			return false
		}
	}
	return true
}
