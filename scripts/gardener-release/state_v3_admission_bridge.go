// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package gardenerrelease

// StateV3SnapshotAdmission classifies one already-authenticated state-lane
// snapshot transition. It is deliberately non-authorizing: callers receive no
// decoded document values or reusable state-machine capability.
type StateV3SnapshotAdmission struct {
	OperationRoot bool
	Staged        bool
}

// ClassifyStateV3AdjacentSnapshot validates an authenticated parent-to-child
// state-lane transition using the same private history predicates used by the
// final state validators. checkpointParent is true only for the reviewed empty
// checkpoint. This bridge neither reads nor persists state and does not decide
// whether a release is authorized.
func ClassifyStateV3AdjacentSnapshot(parent, child StateV3StateSnapshot, policy StateV3Policy, checkpointParent bool) (StateV3SnapshotAdmission, bool) {
	parentRecord, parentOK := validStateV3Snapshot(parent, "", policy, checkpointParent)
	childRecord, childOK := validStateV3Snapshot(child, "", policy, false)
	if !parentOK || !childOK || child.Commit.ParentOID != parent.Commit.OID {
		return StateV3SnapshotAdmission{}, false
	}
	childLease, childLeaseOK := stateV3SnapshotLease(child)
	if !childLeaseOK {
		return StateV3SnapshotAdmission{}, false
	}
	expectedLease := StateV3ActiveOperationLease{}
	if childLease != nil {
		expectedLease = *childLease
	}
	if !validStateV3SnapshotTransition(child, childRecord, parent, parentRecord, child.RecordPath, expectedLease) {
		return StateV3SnapshotAdmission{}, false
	}
	return StateV3SnapshotAdmission{
		OperationRoot: parent.LeasePresent == false && child.LeasePresent && !child.RecordPresent,
		Staged:        child.StagedEnvelope != nil,
	}, true
}
