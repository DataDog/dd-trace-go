// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"

// verifySealedProjectionInputs establishes the prerequisite for a future
// private parent-evidence projection. It proves that each operation-owned,
// compound-sealed modeled leaf has one exact provenance-bound canonical
// document in the store. It deliberately builds no parent graph, returns no
// raw document, and does not invoke semantic validators.
func (op *stateV3AssemblyOperation) verifySealedProjectionInputs() bool {
	if op == nil || op.store == nil || op.policy == nil || !sealedLaneWitnessesMatch(op.seals.minor, stateV3AssemblyMinor, gardenerrelease.MaxStateV3StateLaneHistoryCommits) || !sealedLaneWitnessesMatch(op.seals.patch, stateV3AssemblyPatch, gardenerrelease.MaxStateV3StateLaneHistoryCommits) || !sealedCoordinationWitnessesMatch(op.seals.coordination, gardenerrelease.MaxStateV3CoordinationHistoryCommits) {
		return false
	}
	return op.verifyMinorProjectionDocuments() && op.verifyPatchProjectionDocuments() && op.verifyCoordinationProjectionDocuments()
}

func (op *stateV3AssemblyOperation) verifyMinorProjectionDocuments() bool {
	history := op.seals.minor
	for ordinal := 0; ordinal < int(history.count); ordinal++ {
		witness := history.witnesses[ordinal]
		if !projectionLaneWitnessTopologyValid(witness, stateV3AssemblyMinor) {
			return false
		}
		for entry := 0; entry < int(witness.count); entry++ {
			if !op.store.verifyLaneWitnessDocument(stateV3AssemblyMinor, witness, witness.entries[entry]) {
				return false
			}
		}
	}
	return true
}

func (op *stateV3AssemblyOperation) verifyPatchProjectionDocuments() bool {
	history := op.seals.patch
	for ordinal := 0; ordinal < int(history.count); ordinal++ {
		witness := history.witnesses[ordinal]
		if !projectionLaneWitnessTopologyValid(witness, stateV3AssemblyPatch) {
			return false
		}
		for entry := 0; entry < int(witness.count); entry++ {
			if !op.store.verifyLaneWitnessDocument(stateV3AssemblyPatch, witness, witness.entries[entry]) {
				return false
			}
		}
	}
	return true
}

func (op *stateV3AssemblyOperation) verifyCoordinationProjectionDocuments() bool {
	history := op.seals.coordination
	for ordinal := 0; ordinal < int(history.count); ordinal++ {
		witness := history.witnesses[ordinal]
		if !projectionCoordinationWitnessTopologyValid(witness) {
			return false
		}
		for entry := 0; entry < int(witness.count); entry++ {
			if !op.store.verifyCoordinationWitnessDocument(witness, witness.entries[entry]) {
				return false
			}
		}
	}
	return true
}

func projectionLaneWitnessTopologyValid(witness stateV3FullLaneSnapshotWitness, role stateV3AssemblyRole) bool {
	if witness.role != role || witness.verification != stateV3WitnessVerified || int(witness.count) > len(witness.entries) || witness.treeEmpty != (witness.count == 0) {
		return false
	}
	for index := 1; index < int(witness.count); index++ {
		if fixedWitnessPathCompare(witness.entries[index-1], witness.entries[index]) >= 0 {
			return false
		}
	}
	return true
}

func projectionCoordinationWitnessTopologyValid(witness stateV3FullCoordinationSnapshotWitness) bool {
	if witness.role != stateV3AssemblyCoordination || witness.verification != stateV3WitnessVerified || int(witness.count) > len(witness.entries) || witness.treeEmpty != (witness.count == 0) {
		return false
	}
	for index := 1; index < int(witness.count); index++ {
		if fixedWitnessPathCompare(witness.entries[index-1], witness.entries[index]) >= 0 {
			return false
		}
	}
	return true
}

func fixedWitnessPathCompare(left, right stateV3FullWitnessEntry) int {
	limit := int(left.len)
	if int(right.len) < limit {
		limit = int(right.len)
	}
	for index := 0; index < limit; index++ {
		if left.path[index] < right.path[index] {
			return -1
		}
		if left.path[index] > right.path[index] {
			return 1
		}
	}
	switch {
	case left.len < right.len:
		return -1
	case left.len > right.len:
		return 1
	default:
		return 0
	}
}
