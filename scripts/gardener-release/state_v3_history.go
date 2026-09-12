// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

func stateV3LeaseForReservation(reservation StateV3Reservation) StateV3ActiveOperationLease {
	resolutionDigest, _ := stateV3CanonicalDigest(reservation.VersionResolution)
	claim := reservation.CoordinationClaim
	return StateV3ActiveOperationLease{
		SchemaVersion: "1", RepositoryID: reservation.RepositoryID, RepositoryFullName: reservation.RepositoryFullName,
		OriginalCommentID: reservation.OriginalCommentID, Command: reservation.Command, RequestedVersion: reservation.RequestedVersion,
		ResolvedVersion: reservation.ResolvedVersion, DevelopmentVersion: reservation.DevelopmentVersion, SourceRef: reservation.SourceRef,
		SourceOID: reservation.SourceOID, RequestKey: reservation.RequestKey, RequestSHA256: reservation.RequestSHA256, ReservationMarker: reservation.Marker,
		VersionResolutionSHA256: resolutionDigest, CoordinationRef: StateV3CoordinationRef,
		CoordinationClaimPath: claim.Path, CoordinationClaimOID: claim.Commit.OID,
		CoordinationClaimBlobOID: claim.BlobOID, CoordinationClaimSHA256: claim.SHA256,
		CoordinationParentOID: claim.Commit.ParentOID,
	}
}

func validStateV3Lease(lease StateV3ActiveOperationLease, reservation StateV3Reservation, policy StateV3Policy) bool {
	return validStateV3Reservation(reservation, policy) && lease == stateV3LeaseForReservation(reservation)
}

func validStateV3LeaseShape(lease StateV3ActiveOperationLease, policy StateV3Policy) bool {
	_, commandOK := map[string]bool{"release:prepare": true, "release:promote": true, "release:release": true}[lease.Command]
	return lease.SchemaVersion == "1" && lease.RepositoryID == policy.RepositoryID && lease.RepositoryFullName == policy.RepositoryFullName && validID(lease.OriginalCommentID) && commandOK && validStateV3Text(lease.RequestedVersion, 128) && validStateV3Text(lease.ResolvedVersion, 128) && (lease.DevelopmentVersion == "" || validStateV3Text(lease.DevelopmentVersion, 128)) && strings.HasPrefix(lease.SourceRef, "refs/heads/") && validBranchName(strings.TrimPrefix(lease.SourceRef, "refs/heads/")) && validStateV3OID(lease.SourceOID) && validStateV3Text(lease.RequestKey, 256) && lowerHexDigest(lease.RequestSHA256) && validStateV3Text(lease.ReservationMarker, 512) && lowerHexDigest(lease.VersionResolutionSHA256) && lease.CoordinationRef == policy.Coordination.StateRef && validStateV3CoordinationPath(lease.CoordinationClaimPath) && validStateV3OID(lease.CoordinationClaimOID) && validStateV3OID(lease.CoordinationClaimBlobOID) && lowerHexDigest(lease.CoordinationClaimSHA256) && validStateV3OID(lease.CoordinationParentOID)
}

func stateV3SnapshotLease(snapshot StateV3StateSnapshot) (*StateV3ActiveOperationLease, bool) {
	if !snapshot.LeasePresent {
		return nil, snapshot.LeasePath == "" && len(snapshot.RawLease) == 0 && snapshot.LeaseSHA256 == "" && snapshot.LeaseBlobOID == ""
	}
	if snapshot.LeasePath != StateV3ActiveLeasePath || !lowerHexDigest(snapshot.LeaseSHA256) || !validStateV3OID(snapshot.LeaseBlobOID) || stateV3GitBlobOID(snapshot.RawLease) != snapshot.LeaseBlobOID {
		return nil, false
	}
	digest := sha256.Sum256(snapshot.RawLease)
	if hex.EncodeToString(digest[:]) != snapshot.LeaseSHA256 {
		return nil, false
	}
	var lease StateV3ActiveOperationLease
	if decodeStateV3Document(snapshot.RawLease, MaxStateV3DocumentBytes, &lease) != nil {
		return nil, false
	}
	canonical, err := canonicalJSON(lease)
	if err != nil || !bytes.Equal(canonical, snapshot.RawLease) {
		return nil, false
	}
	return &lease, true
}

// ValidateStateV3ActiveLease authenticates the crash-recoverable lease-only
// state written before the reservation record. Only the bound operation may
// continue; administrative deletion is deliberately outside this workflow contract.
func ValidateStateV3ActiveLease(raw []byte, lease StateV3ActiveOperationLease, reservation StateV3Reservation, policy StateV3Policy, authentication StateV3Authentication) error {
	invalid := func() error { return newReleaseError(ErrorClassStateConflict, "invalid_state_v3_lease") }
	canonical, err := canonicalJSON(lease)
	lane, laneOK := stateV3LaneForReservation(reservation, policy)
	if err != nil || !laneOK || validateStateV3Policy(policy) != nil || !bytes.Equal(raw, canonical) || !validStateV3Lease(lease, reservation, policy) || authentication.StateRef != lane.StateRef || authentication.CheckpointOID != lane.CheckpointOID || authentication.HeadOID != authentication.Current.Commit.OID || !authentication.Current.LeasePresent || authentication.Current.RecordPresent || !bytes.Equal(authentication.Current.RawLease, raw) || len(authentication.Predecessors) == 0 || !stateV3CapacityFits(len(authentication.Predecessors)+1, stateV3WorstRemainingAfterLease(reservation.Command), lane.MaxHistoryCommits) {
		return invalid()
	}
	paths, ok := stateV3PreparedPaths(reservation.RepositoryID, reservation.OriginalCommentID)
	if !ok {
		return invalid()
	}
	recordPath := strings.TrimSuffix(paths.Reservation, stateV3ReservationFile) + "state.json"
	snapshots := append([]StateV3StateSnapshot{authentication.Current}, authentication.Predecessors...)
	seen := map[string]bool{}
	for index := range snapshots {
		checkpoint := index == len(snapshots)-1
		record, valid := validStateV3Snapshot(snapshots[index], recordPath, policy, checkpoint)
		if !valid || record != nil || seen[snapshots[index].Commit.OID] {
			return invalid()
		}
		seen[snapshots[index].Commit.OID] = true
		if checkpoint {
			if snapshots[index].Commit.OID != lane.CheckpointOID {
				return invalid()
			}
		} else if snapshots[index].Commit.ParentOID != snapshots[index+1].Commit.OID {
			return invalid()
		}
		if index+1 < len(snapshots) {
			changes := stateV3TreeChanges(snapshots[index+1].Tree, snapshots[index].Tree)
			if !reflect.DeepEqual(changes, snapshots[index].Commit.ChangedPaths) {
				return invalid()
			}
		}
	}
	observedLease, leaseOK := stateV3SnapshotLease(authentication.Current)
	if !leaseOK || observedLease == nil || *observedLease != lease || authentication.Current.ActiveRecord.Present || !validStateV3LaneLifecycles(snapshots, policy, lane) || !validStateV3CrossLaneView(reservation, policy, authentication, authentication.CoordinationOrZero()) {
		return invalid()
	}
	return nil
}

func stateV3SnapshotActiveRecord(snapshot StateV3StateSnapshot, policy StateV3Policy) (*StateV3Record, bool) {
	evidence := snapshot.ActiveRecord
	if !evidence.Present {
		return nil, evidence.Path == "" && len(evidence.Raw) == 0 && evidence.SHA256 == "" && evidence.BlobOID == "" && evidence.StagedEnvelope == nil
	}
	if !validStateV3StatePath(evidence.Path) || !strings.HasSuffix(evidence.Path, "/state.json") || !lowerHexDigest(evidence.SHA256) || !validStateV3OID(evidence.BlobOID) || stateV3GitBlobOID(evidence.Raw) != evidence.BlobOID {
		return nil, false
	}
	digest := sha256.Sum256(evidence.Raw)
	if hex.EncodeToString(digest[:]) != evidence.SHA256 {
		return nil, false
	}
	projected := snapshot
	projected.RecordPresent = true
	projected.RecordPath = evidence.Path
	projected.RawRecord = evidence.Raw
	projected.RecordSHA256 = evidence.SHA256
	projected.RecordBlobOID = evidence.BlobOID
	projected.StagedEnvelope = evidence.StagedEnvelope
	record, valid := validStateV3Snapshot(projected, evidence.Path, policy, false)
	if !valid || record == nil {
		return nil, false
	}
	paths, ok := stateV3PreparedPaths(record.Reservation.RepositoryID, record.Reservation.OriginalCommentID)
	return record, ok && evidence.Path == strings.TrimSuffix(paths.Reservation, stateV3ReservationFile)+"state.json"
}

func stateV3LeaseLane(lease StateV3ActiveOperationLease, policy StateV3Policy) (StateV3LanePolicy, bool) {
	version, err := ParseReleaseVersion(lease.ResolvedVersion)
	if err != nil {
		return StateV3LanePolicy{}, false
	}
	if version.Patch == 0 {
		return policy.StateLanes.Minor, true
	}
	return policy.StateLanes.Patch, true
}

func validStateV3LaneLifecycles(snapshots []StateV3StateSnapshot, policy StateV3Policy, lane StateV3LanePolicy) bool {
	chronological := append([]StateV3StateSnapshot(nil), snapshots...)
	for left, right := 0, len(chronological)-1; left < right; left, right = left+1, right-1 {
		chronological[left], chronological[right] = chronological[right], chronological[left]
	}
	var previousLease *StateV3ActiveOperationLease
	var previousRecord *StateV3Record
	for index, snapshot := range chronological {
		lease, leaseOK := stateV3SnapshotLease(snapshot)
		record, recordOK := stateV3SnapshotActiveRecord(snapshot, policy)
		if !leaseOK || !recordOK || lease != nil && (!validStateV3LeaseShape(*lease, policy) || func() bool {
			selected, ok := stateV3LeaseLane(*lease, policy)
			return !ok || selected.StateRef != lane.StateRef
		}()) {
			return false
		}
		if index == 0 {
			if lease != nil || record != nil {
				return false
			}
			continue
		}
		parent := chronological[index-1]
		changes := snapshot.Commit.ChangedPaths
		switch {
		case previousLease == nil && lease == nil:
			return false // Ordinary lane history advances only by lease acquisition.
		case previousLease == nil && lease != nil:
			if record != nil || len(changes) != 1 || changes[0] != (StateV3ChangedPath{Path: StateV3ActiveLeasePath, ChildOID: snapshot.LeaseBlobOID}) {
				return false
			}
		case previousLease != nil && lease != nil:
			if *lease != *previousLease || record == nil || !validStateV3Lease(*lease, record.Reservation, policy) {
				return false
			}
			childProjected, parentProjected := stateV3ProjectActiveSnapshot(snapshot), stateV3ProjectActiveSnapshot(parent)
			if !validStateV3SnapshotTransition(childProjected, record, parentProjected, previousRecord, record.ActiveRecordPath(), *lease) {
				return false
			}
		case previousLease != nil && lease == nil:
			if record == nil || previousRecord == nil || record.Phase != StateV3PhaseComplete || !reflect.DeepEqual(record, previousRecord) || !validStateV3Lease(*previousLease, record.Reservation, policy) || len(changes) != 1 || changes[0] != (StateV3ChangedPath{Path: StateV3ActiveLeasePath, ParentOID: parent.LeaseBlobOID}) {
				return false
			}
		}
		previousLease, previousRecord = lease, record
	}
	return true
}

func (record StateV3Record) ActiveRecordPath() string {
	paths, ok := stateV3PreparedPaths(record.Reservation.RepositoryID, record.Reservation.OriginalCommentID)
	if !ok {
		return ""
	}
	return strings.TrimSuffix(paths.Reservation, stateV3ReservationFile) + "state.json"
}

func stateV3ProjectActiveSnapshot(snapshot StateV3StateSnapshot) StateV3StateSnapshot {
	evidence := snapshot.ActiveRecord
	snapshot.RecordPresent = evidence.Present
	snapshot.RecordPath = evidence.Path
	snapshot.RawRecord = evidence.Raw
	snapshot.RecordSHA256 = evidence.SHA256
	snapshot.RecordBlobOID = evidence.BlobOID
	snapshot.StagedEnvelope = evidence.StagedEnvelope
	return snapshot
}

func validStateV3Authentication(authentication StateV3Authentication, policy StateV3Policy, current StateV3Record) bool {
	paths, ok := stateV3PreparedPaths(current.Reservation.RepositoryID, current.Reservation.OriginalCommentID)
	lane, laneOK := stateV3LaneForReservation(current.Reservation, policy)
	if !ok || !laneOK {
		return false
	}
	recordPath := strings.TrimSuffix(paths.Reservation, stateV3ReservationFile) + "state.json"
	if authentication.StateRef != lane.StateRef || authentication.CheckpointOID != lane.CheckpointOID || authentication.HeadOID != authentication.Current.Commit.OID || !validStateV3OID(authentication.HeadOID) || len(authentication.Predecessors) == 0 || len(authentication.Predecessors)+1 > lane.MaxHistoryCommits {
		return false
	}
	snapshots := append([]StateV3StateSnapshot{authentication.Current}, authentication.Predecessors...)
	records := make([]*StateV3Record, len(snapshots))
	seen := map[string]bool{}
	expectedLease := stateV3LeaseForReservation(current.Reservation)
	for index := range snapshots {
		checkpoint := index == len(snapshots)-1
		snapshotRecord, valid := validStateV3Snapshot(snapshots[index], recordPath, policy, checkpoint)
		lease, leaseValid := stateV3SnapshotLease(snapshots[index])
		if !leaseValid || lease != nil && !validStateV3LeaseShape(*lease, policy) {
			return false
		}
		if !valid || seen[snapshots[index].Commit.OID] {
			return false
		}
		records[index] = snapshotRecord
		seen[snapshots[index].Commit.OID] = true
		if checkpoint {
			if snapshots[index].Commit.OID != lane.CheckpointOID {
				return false
			}
		} else if snapshots[index].Commit.ParentOID != snapshots[index+1].Commit.OID {
			return false
		}
	}
	currentRaw, currentErr := canonicalJSON(current)
	recordRaw := []byte(nil)
	if records[0] != nil {
		recordRaw, _ = canonicalJSON(*records[0])
	}
	if currentErr != nil || records[0] == nil || !bytes.Equal(recordRaw, currentRaw) || snapshots[len(snapshots)-1].LeasePresent || !validStateV3LaneLifecycles(snapshots, policy, lane) {
		return false
	}
	leaseAcquisitions, leaseReleases := 0, 0
	for index := 0; index+1 < len(snapshots); index++ {
		childLease, _ := stateV3SnapshotLease(snapshots[index])
		parentLease, _ := stateV3SnapshotLease(snapshots[index+1])
		if childLease != nil && *childLease == expectedLease && (parentLease == nil || *parentLease != expectedLease) {
			leaseAcquisitions++
		}
		if parentLease != nil && *parentLease == expectedLease && (childLease == nil || *childLease != expectedLease) {
			leaseReleases++
		}
		derived := stateV3TreeChanges(snapshots[index+1].Tree, snapshots[index].Tree)
		if !reflect.DeepEqual(derived, snapshots[index].Commit.ChangedPaths) || !validStateV3SnapshotTransition(snapshots[index], records[index], snapshots[index+1], records[index+1], recordPath, expectedLease) {
			return false
		}
	}
	if leaseAcquisitions != 1 || current.Phase == StateV3PhaseComplete && !authentication.Current.LeasePresent && leaseReleases != 1 || (current.Phase != StateV3PhaseComplete || authentication.Current.LeasePresent) && leaseReleases != 0 || current.Phase != StateV3PhaseComplete && !authentication.Current.LeasePresent {
		return false
	}
	remaining, capacityKnown := stateV3RemainingOperationCommits(current, authentication.Current)
	return capacityKnown && stateV3CapacityFits(len(snapshots), remaining, lane.MaxHistoryCommits)
}

// stateV3RemainingOperationCommits reserves exact remaining depth while the
// authenticated lane-local lease excludes every unrelated commit on that lane.
func stateV3CapacityFits(currentDepth, remaining, maximum int) bool {
	return currentDepth >= 0 && remaining >= 0 && maximum >= 0 && currentDepth <= maximum-remaining
}

func stateV3WorstRemainingAfterLease(command string) int {
	if command == "release:prepare" {
		return 4*MaxStateV3Tags + 19 // reservation, staging, all events, and release
	}
	return 4*MaxStateV3Tags + 13
}

func stateV3RemainingOperationCommits(record StateV3Record, snapshot StateV3StateSnapshot) (int, bool) {
	plans := record.TagPlans
	stageCommitRemaining := 0
	if len(plans) == 0 {
		if snapshot.StagedEnvelope == nil {
			plans = make([]StateV3TagPlan, MaxStateV3Tags)
		} else {
			plans = snapshot.StagedEnvelope.TagPlans
		}
	}
	if snapshot.StagedEnvelope == nil {
		stageCommitRemaining = 1
	}
	totalEvents := 4*len(plans) + 11
	if record.Reservation.Command == "release:prepare" {
		totalEvents = 4*len(plans) + 17
	}
	if record.Commit != nil && record.Commit.MutationResponse.Observation == "not_attempted" {
		totalEvents--
	}
	for _, branch := range record.BranchEvidence {
		if branch.Intent.Target == "source" && branch.Response.Observation == "not_attempted" {
			totalEvents--
		}
	}
	for _, object := range record.TagObjectEvidence {
		if object.Response.Observation == "not_attempted" {
			totalEvents--
		}
	}
	for _, ref := range record.TagEvidence {
		if ref.RefResponse.Observation == "not_attempted" {
			totalEvents--
		}
	}
	if record.PreparePREvidence != nil && record.PreparePREvidence.Response.Observation == "not_attempted" {
		totalEvents--
	}
	if len(record.Events) > totalEvents {
		return 0, true
	}
	leaseReleaseRemaining := 0
	if snapshot.LeasePresent {
		leaseReleaseRemaining = 1
	}
	return totalEvents - len(record.Events) + stageCommitRemaining + leaseReleaseRemaining, true
}

func validStateV3Snapshot(snapshot StateV3StateSnapshot, recordPath string, policy StateV3Policy, checkpoint bool) (*StateV3Record, bool) {
	commit := snapshot.Commit
	if !validStateV3OID(commit.OID) || !validStateV3OID(commit.TreeOID) || !checkpoint && (!validStateV3OID(commit.ParentOID) || commit.OID == commit.ParentOID) || !commit.RESTVerified || commit.RESTReason != StateV3RequiredRESTVerificationReason || !commit.GraphQLSignatureValid || !commit.WasSignedByGitHub || commit.SignatureState != StateV3RequiredSignatureState || commit.Roles != policy.CommitRoles {
		return nil, false
	}
	if !checkpoint && !validStateV3StateChanges(commit.ChangedPaths) || checkpoint && len(commit.ChangedPaths) != 0 {
		return nil, false
	}
	if snapshot.Tree.OID != commit.TreeOID || !snapshot.Tree.Complete || snapshot.Tree.Truncated || len(snapshot.Tree.Entries) > 10_000 {
		return nil, false
	}
	entryFound := false
	leaseEntryFound := false
	recordPathSeen := false
	entries := map[string]string{}
	previous := ""
	for _, entry := range snapshot.Tree.Entries {
		if entry.Path <= previous || !validStateV3StatePath(entry.Path) || entry.Mode != "100644" || entry.Type != "blob" || !validStateV3OID(entry.OID) {
			return nil, false
		}
		previous = entry.Path
		entries[entry.Path] = entry.OID
		if entry.Path == recordPath {
			recordPathSeen = true
			entryFound = entry.OID == snapshot.RecordBlobOID
		}
		if entry.Path == StateV3ActiveLeasePath {
			leaseEntryFound = entry.OID == snapshot.LeaseBlobOID
		}
	}
	lease, leaseValid := stateV3SnapshotLease(snapshot)
	if !leaseValid || snapshot.LeasePresent != leaseEntryFound || lease != nil && lease.SchemaVersion != "1" {
		return nil, false
	}
	if !snapshot.RecordPresent {
		return nil, !recordPathSeen && snapshot.RecordPath == "" && len(snapshot.RawRecord) == 0 && snapshot.RecordSHA256 == "" && snapshot.RecordBlobOID == "" && snapshot.StagedEnvelope == nil
	}
	if snapshot.RecordPath != recordPath || !entryFound || !lowerHexDigest(snapshot.RecordSHA256) || !validStateV3OID(snapshot.RecordBlobOID) {
		return nil, false
	}
	digest := sha256.Sum256(snapshot.RawRecord)
	blobOID := stateV3GitBlobOID(snapshot.RawRecord)
	if snapshot.RecordSHA256 != hex.EncodeToString(digest[:]) || snapshot.RecordBlobOID != blobOID {
		return nil, false
	}
	decoded, err := DecodeStateV3Record(snapshot.RawRecord)
	if err != nil {
		return nil, false
	}
	canonical, err := canonicalJSON(decoded)
	if err != nil || !bytes.Equal(canonical, snapshot.RawRecord) || validateStateV3RecordCore(decoded, policy) != nil {
		return nil, false
	}
	root := strings.TrimSuffix(recordPath, "state.json")
	expected := map[string]string{recordPath: snapshot.RecordBlobOID}
	if snapshot.LeasePresent {
		expected[StateV3ActiveLeasePath] = snapshot.LeaseBlobOID
	}
	if snapshot.StagedEnvelope != nil {
		if !validStateV3StagedEnvelope(*snapshot.StagedEnvelope, decoded.Reservation, policy) {
			return nil, false
		}
		for _, file := range snapshot.StagedEnvelope.Files {
			expected[file.Path] = file.BlobOID
		}
	}
	if decoded.Prepared != nil {
		if snapshot.StagedEnvelope == nil || !reflect.DeepEqual(decoded.Prepared, &snapshot.StagedEnvelope.Prepared) || !reflect.DeepEqual(decoded.TagPlans, snapshot.StagedEnvelope.TagPlans) {
			return nil, false
		}
	} else if snapshot.StagedEnvelope != nil && decoded.Phase != StateV3PhaseReserved {
		return nil, false
	}
	for path, oid := range entries {
		if strings.HasPrefix(path, root) && expected[path] != oid {
			return nil, false
		}
	}
	for path, oid := range expected {
		if entries[path] != oid {
			return nil, false
		}
	}
	return &decoded, true
}

func validStateV3SnapshotTransition(childSnapshot StateV3StateSnapshot, child *StateV3Record, parentSnapshot StateV3StateSnapshot, parent *StateV3Record, recordPath string, expectedLease StateV3ActiveOperationLease) bool {
	changes := childSnapshot.Commit.ChangedPaths
	childLease, childLeaseOK := stateV3SnapshotLease(childSnapshot)
	parentLease, parentLeaseOK := stateV3SnapshotLease(parentSnapshot)
	if !childLeaseOK || !parentLeaseOK {
		return false
	}
	// Before acquisition, older completed operations may advance this lane ref.
	if childLease == nil && parentLease == nil {
		return child == nil && parent == nil && len(changes) > 0
	}
	// Every lane-local acquisition is a dedicated lease-only commit.
	if childLease != nil && parentLease == nil {
		return child == nil && parent == nil && len(changes) == 1 && changes[0] == (StateV3ChangedPath{Path: StateV3ActiveLeasePath, ChildOID: childSnapshot.LeaseBlobOID})
	}
	// The current operation's release proves complete, byte-identical state. A
	// prior operation's release is still exactly a one-path lease deletion.
	if childLease == nil && parentLease != nil {
		if *parentLease != expectedLease {
			return child == nil && parent == nil && len(changes) == 1 && changes[0] == (StateV3ChangedPath{Path: StateV3ActiveLeasePath, ParentOID: parentSnapshot.LeaseBlobOID})
		}
		return child != nil && parent != nil && child.Phase == StateV3PhaseComplete && reflect.DeepEqual(child, parent) && reflect.DeepEqual(childSnapshot.StagedEnvelope, parentSnapshot.StagedEnvelope) && len(changes) == 1 && changes[0] == (StateV3ChangedPath{Path: StateV3ActiveLeasePath, ParentOID: parentSnapshot.LeaseBlobOID})
	}
	if childLease == nil || parentLease == nil || *childLease != *parentLease {
		return false
	}
	if *childLease != expectedLease {
		if child != nil || parent != nil || len(changes) == 0 {
			return false
		}
		root := "requests/" + childLease.RepositoryID + "/" + childLease.OriginalCommentID + "/"
		for _, change := range changes {
			if !strings.HasPrefix(change.Path, root) {
				return false
			}
		}
		return true
	}
	recordChanged := containsStateV3Change(changes, recordPath)
	if child == nil {
		return false // No commit may interleave between lease acquisition and reservation.
	}
	if parent == nil {
		return recordChanged && len(changes) == 1 && child.Phase == StateV3PhaseReserved && len(child.Events) == 1 && childSnapshot.StagedEnvelope == nil
	}
	if !reflect.DeepEqual(child.Reservation, parent.Reservation) || child.Binding != parent.Binding || parent.Prepared != nil && !reflect.DeepEqual(child.Prepared, parent.Prepared) || len(parent.TagPlans) != 0 && !reflect.DeepEqual(child.TagPlans, parent.TagPlans) || len(child.Events) < len(parent.Events) || !reflect.DeepEqual(child.Events[:len(parent.Events)], parent.Events) {
		return false
	}
	if recordChanged {
		if len(changes) != 1 || changes[0].Path != recordPath || len(child.Events) != len(parent.Events)+1 || parentSnapshot.StagedEnvelope == nil || !reflect.DeepEqual(childSnapshot.StagedEnvelope, parentSnapshot.StagedEnvelope) {
			return false
		}
		if child.Events[len(child.Events)-1].Kind == StateV3EventPhaseAdvanced && child.Events[len(child.Events)-1].Phase == StateV3PhasePrepared {
			return parent.Phase == StateV3PhaseReserved && reflect.DeepEqual(child.Prepared, &childSnapshot.StagedEnvelope.Prepared) && reflect.DeepEqual(child.TagPlans, childSnapshot.StagedEnvelope.TagPlans)
		}
		return parent.Prepared != nil
	}
	if !reflect.DeepEqual(child, parent) || parentSnapshot.StagedEnvelope == nil && childSnapshot.StagedEnvelope == nil {
		return false
	}
	if parentSnapshot.StagedEnvelope == nil && childSnapshot.StagedEnvelope != nil {
		if child.Phase != StateV3PhaseReserved || len(changes) != StateV3PreparedAdditionCount {
			return false
		}
		for index, change := range changes {
			if change.Path != childSnapshot.StagedEnvelope.Files[index].Path || change.ParentOID != "" || change.ChildOID != childSnapshot.StagedEnvelope.Files[index].BlobOID {
				return false
			}
		}
		return true
	}
	return false // While held, no unrelated or byte-identical interleaving commit is valid.
}

func containsStateV3Change(changes []StateV3ChangedPath, want string) bool {
	index := sort.Search(len(changes), func(index int) bool { return changes[index].Path >= want })
	return index < len(changes) && changes[index].Path == want
}

func stateV3TreeChanges(parent, child StateV3StateTreeEvidence) []StateV3ChangedPath {
	parentEntries, childEntries := map[string]string{}, map[string]string{}
	for _, entry := range parent.Entries {
		parentEntries[entry.Path] = entry.OID
	}
	for _, entry := range child.Entries {
		childEntries[entry.Path] = entry.OID
	}
	paths := map[string]bool{}
	for path := range parentEntries {
		paths[path] = true
	}
	for path := range childEntries {
		paths[path] = true
	}
	result := make([]StateV3ChangedPath, 0)
	for path := range paths {
		if parentEntries[path] != childEntries[path] {
			result = append(result, StateV3ChangedPath{Path: path, ParentOID: parentEntries[path], ChildOID: childEntries[path]})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}

func stateV3GitBlobOID(raw []byte) string {
	hasher := sha1.New() //nolint:gosec // This computes a Git SHA-1 object ID.
	_, _ = fmt.Fprintf(hasher, "blob %d%c", len(raw), byte(0))
	_, _ = hasher.Write(raw)
	return hex.EncodeToString(hasher.Sum(nil))
}

func validStateV3StateChanges(changes []StateV3ChangedPath) bool {
	if len(changes) == 0 || len(changes) > StateV3PreparedAdditionCount || !sort.SliceIsSorted(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path }) {
		return false
	}
	seen := map[string]bool{}
	for _, change := range changes {
		if !validStateV3StatePath(change.Path) || seen[change.Path] || change.ParentOID == "" && change.ChildOID == "" || change.ParentOID != "" && !validStateV3OID(change.ParentOID) || change.ChildOID != "" && !validStateV3OID(change.ChildOID) {
			return false
		}
		seen[change.Path] = true
	}
	return true
}

func validStateV3StatePath(path string) bool {
	if path == StateV3ActiveLeasePath {
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) != 4 || parts[0] != "requests" || !validID(parts[1]) || !validID(parts[2]) {
		return false
	}
	switch parts[3] {
	case "state.json", stateV3ReservationFile, stateV3PreparedFile, stateV3GenerationBundleFile:
		return true
	default:
		return false
	}
}

func (authentication StateV3Authentication) CoordinationOrZero() StateV3CoordinationAuthentication {
	if authentication.Coordination == nil {
		return StateV3CoordinationAuthentication{}
	}
	return *authentication.Coordination
}
