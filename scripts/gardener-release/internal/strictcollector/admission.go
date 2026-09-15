// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"encoding/hex"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

// stateV3AuthenticatedSnapshot is a transient semantic projection for one
// chronological comparison. It is not an authority for document reads or
// document-store admission; those operations resolve only session-owned
// compact-history slots.
type stateV3AuthenticatedSnapshot struct {
	ordinal   uint16
	projected gardenerrelease.StateV3StateSnapshot
}

// stateV3LaneHistoryAdmission is intentionally distinct from the immutable
// document cache. It represents chronological acceptance, not cache insertion
// order. It is invalidated when its owning child finishes.
type stateV3LaneHistoryAdmission struct {
	child      *stateV3AssemblyChild
	generation uint64
	closed     bool
}

// stateV3DocumentAdmission is a one-entry, one-snapshot capability. All
// identity fields are copied from authenticated strictcollector artifacts.
type stateV3DocumentAdmission struct {
	owner      *stateV3LaneHistoryAdmission
	generation uint64
	ordinal    uint16
	role       stateV3AssemblyRole
	commitOID  [20]byte
	treeOID    [20]byte
	index      int
	kind       stateV3DocumentKind
	path       string
	blobOID    [20]byte
}

func (a *stateV3LaneHistoryAdmission) close() {
	if a != nil {
		a.closed = true
		a.generation++
	}
}

// admitCompact issues an entry capability from a compacted authenticated
// snapshot. Unlike ordinary approval it has no live raw/tree artifacts: both
// identities were copied from the authenticated artifacts before release.
func (a *stateV3LaneHistoryAdmission) admitCompact(ordinal uint16, document int) (stateV3DocumentAdmission, bool) {
	var token stateV3DocumentAdmission
	if a == nil || a.closed || a.child == nil || a.child.session == nil {
		return token, false
	}
	entry, ok := a.child.session.compactEntry(a.generation, ordinal, document)
	if !ok {
		return token, false
	}
	commitOID, commitOK := decodeFixedOID(entry.commitOID)
	treeOID, treeOK := decodeFixedOID(entry.treeOID)
	blobOID, blobOK := decodeFixedOID(entry.oid)
	if !commitOK || !treeOK || !blobOK {
		return token, false
	}
	return stateV3DocumentAdmission{owner: a, generation: a.generation, ordinal: ordinal, role: entry.role, commitOID: commitOID, treeOID: treeOID, index: entry.index, kind: entry.kind, path: entry.path, blobOID: blobOID}, true
}

func hexFixedOID(value [20]byte) string {
	return hex.EncodeToString(value[:])
}

func (t stateV3DocumentAdmission) permits(entry stateV3ApprovedDocumentEntry) bool {
	if t.owner == nil || t.owner.closed || t.generation != t.owner.generation || t.owner.child == nil {
		return false
	}
	commitOID, commitOK := decodeFixedOID(entry.commitOID)
	treeOID, treeOK := decodeFixedOID(entry.treeOID)
	blobOID, blobOK := decodeFixedOID(entry.oid)
	return commitOK && treeOK && blobOK && t.role == entry.role && t.commitOID == commitOID && t.treeOID == treeOID && t.index == entry.index && t.kind == entry.kind && t.path == entry.path && t.blobOID == blobOID
}

// classifyAdjacentSnapshot delegates state-lane transition semantics to the
// parent package. It deliberately returns only a small classification and no
// decoded document or authentication result.
func (a *stateV3LaneHistoryAdmission) classifyAdjacentSnapshot(parent, child stateV3AuthenticatedSnapshot, policy gardenerrelease.StateV3Policy, checkpointParent bool) (gardenerrelease.StateV3SnapshotAdmission, bool) {
	// Compact spines are stored newest first while admission runs checkpoint to
	// head, so a chronological child has the immediately lower stored ordinal.
	if a == nil || a.closed || parent.ordinal != child.ordinal+1 {
		return gardenerrelease.StateV3SnapshotAdmission{}, false
	}
	// The role-bound checkpoint/history window admits at most the parent-derived
	// number of operation roots. A separate root counter cannot be reached for
	// a valid 27th root within that fixed window, so it is deliberately not an
	// independent authority or capacity gate.
	return gardenerrelease.ClassifyStateV3AdjacentSnapshot(parent.projected, child.projected, policy, checkpointParent)
}
