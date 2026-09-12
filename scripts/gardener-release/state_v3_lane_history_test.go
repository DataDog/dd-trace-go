// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"encoding/json"
	"fmt"
	"sort"
	"testing"
)

func TestStateV3LaneSelectionAndLeaseSerialization(t *testing.T) {
	policy := fixtureStateV3Policy()
	minorRecord := fixtureStateV3Record(t)
	minorLane, ok := stateV3LaneForReservation(minorRecord.Reservation, policy)
	if !ok || minorLane != policy.StateLanes.Minor {
		t.Fatal("zero-patch finalized target did not derive the minor lane")
	}
	patchReservation := minorRecord.Reservation
	patchReservation.ResolvedVersion = "v2.11.1-rc.1"
	patchLane, ok := stateV3LaneForReservation(patchReservation, policy)
	if !ok || patchLane != policy.StateLanes.Patch || patchLane.StateRef == minorLane.StateRef {
		t.Fatal("positive-patch finalized target did not derive the independent patch lane")
	}
	invalid := patchReservation
	invalid.ResolvedVersion = "caller-lane"
	if _, ok := stateV3LaneForReservation(invalid, policy); ok {
		t.Fatal("caller-selected/non-SemVer lane accepted")
	}
	patchRecord := fixtureStateV3RecordAtEventCount(t, minorRecord, 1)
	patchRecord.Reservation.RequestedVersion = "v2.11.1"
	patchRecord.Reservation.ResolvedVersion = "v2.11.1-rc.1"
	patchRecord.Reservation.GenerationVersion = "v2.11.1-rc.1"
	patchRecord.Reservation.BodySnapshot = "/gardener release:promote v2.11.1"
	patchRecord.Reservation.Marker = Marker("123", "789", "release:promote", "v2.11.1")
	patchContext := Context{RepositoryID: "123", RepositoryFullName: RepositoryFullName, IssueNumber: "456", OriginalCommentID: "789", AcknowledgementCommentID: "790", BodySnapshot: patchRecord.Reservation.BodySnapshot, PolicyRevision: patchRecord.Reservation.PolicyRevision}
	patchRecord.Reservation.RequestSHA256 = RequestSHA256(patchContext, "release:promote", "v2.11.1")
	patchRecord.Binding.RequestSHA256 = patchRecord.Reservation.RequestSHA256
	fixtureStateV3VersionResolution(t, &patchRecord.Reservation, "v2.11.0-dev")
	rebindStateV3Events(t, &patchRecord)
	patchRaw, _ := canonicalJSON(patchRecord)
	patchAuth := fixtureStateV3Authentication(t, policy, patchRecord)
	if patchAuth.StateRef != StateV3PatchStateRef || patchAuth.CheckpointOID != policy.StateLanes.Patch.CheckpointOID {
		t.Fatal("patch operation did not use its independent protected ref/checkpoint")
	}
	if err := ValidateStateV3Record(patchRaw, patchRecord, policy, patchAuth); err != nil {
		t.Fatalf("valid patch-lane reservation rejected: %v", err)
	}

	active := fixtureStateV3RecordAtEventCount(t, minorRecord, 3)
	raw, _ := canonicalJSON(active)
	authentication := fixtureStateV3Authentication(t, policy, active)

	// A second operation cannot replace the fixed lane-local lease.
	contended := cloneStateV3(t, authentication)
	previous := contended.Current
	current := cloneStateV3(t, previous)
	otherLease := stateV3LeaseForReservation(active.Reservation)
	otherLease.OriginalCommentID = "999"
	otherLease.RequestKey = "123:999"
	fixtureStateV3RemoveLease(&current)
	fixtureStateV3AddLease(t, &current, otherLease)
	current.Commit.OID = fmt.Sprintf("%040x", 930001)
	current.Commit.ParentOID = previous.Commit.OID
	current.Commit.TreeOID = fmt.Sprintf("%040x", 930002)
	current.Tree.OID = current.Commit.TreeOID
	current.Commit.ChangedPaths = stateV3TreeChanges(previous.Tree, current.Tree)
	contended.Current = current
	contended.HeadOID = current.Commit.OID
	contended.Predecessors = append([]StateV3StateSnapshot{previous}, contended.Predecessors...)
	if err := ValidateStateV3Record(raw, active, policy, contended); err == nil {
		t.Fatal("same-lane lease contention accepted")
	}

	// An unmatched release mutation arm remains globally blocking within its lane.
	blocked := cloneStateV3(t, authentication)
	previous = blocked.Current
	current = cloneStateV3(t, previous)
	current.Commit.OID = fmt.Sprintf("%040x", 930003)
	current.Commit.ParentOID = previous.Commit.OID
	current.Commit.TreeOID = fmt.Sprintf("%040x", 930004)
	current.Tree.OID = current.Commit.TreeOID
	current.Tree.Entries = append(current.Tree.Entries, StateV3StateTreeEntry{Path: "requests/123/999/state.json", Mode: "100644", Type: "blob", OID: v3OIDd})
	sort.Slice(current.Tree.Entries, func(i, j int) bool { return current.Tree.Entries[i].Path < current.Tree.Entries[j].Path })
	current.Commit.ChangedPaths = stateV3TreeChanges(previous.Tree, current.Tree)
	blocked.Current = current
	blocked.HeadOID = current.Commit.OID
	blocked.Predecessors = append([]StateV3StateSnapshot{previous}, blocked.Predecessors...)
	if err := ValidateStateV3Record(raw, active, policy, blocked); err == nil {
		t.Fatal("unrelated operation advanced lane after terminal ambiguous arm")
	}

	// A fully authenticated, completed, and released operation may precede a later acquisition.
	prior := fixtureStateV3Authentication(t, policy, minorRecord)
	priorRaw, _ := canonicalJSON(minorRecord)
	if err := ValidateStateV3Record(priorRaw, minorRecord, policy, prior); err != nil {
		t.Fatalf("older complete record was not inspectable at its retained release commit: %v", err)
	}
	reserved := fixtureStateV3ReservedForComment(t, "998")
	reserved.Reservation.VersionResolution.Coordination = fixtureStateV3ReleasedCoordination(t, minorRecord)
	fixtureStateV3CoordinationClaim(t, &reserved.Reservation)
	fixtureStateV3BindPriorLaneHead(t, policy, prior, &reserved)
	later := fixtureStateV3AfterHistory(t, policy, prior, reserved, false)
	reservedRaw, _ := canonicalJSON(reserved)
	if err := ValidateStateV3Record(reservedRaw, reserved, policy, later); err != nil {
		t.Fatalf("later operation after prior released history rejected: %v", err)
	}
}

func TestStateV3RejectsUnauthenticatedPriorLeaseLifecycles(t *testing.T) {
	policy := fixtureStateV3Policy()
	full := fixtureStateV3Record(t)
	current := fixtureStateV3ReservedForComment(t, "998")
	currentRaw, _ := canonicalJSON(current)

	for name, eventCount := range map[string]int{"reserved": 1, "prepared": 2, "unmatched arm": 3} {
		t.Run("early release "+name, func(t *testing.T) {
			prefix := fixtureStateV3RecordAtEventCount(t, full, eventCount)
			prior := fixtureStateV3Authentication(t, policy, prefix)
			parent := prior.Current
			release := cloneStateV3(t, parent)
			fixtureStateV3RemoveLease(&release)
			release.Commit.OID = fmt.Sprintf("%040x", 980000+eventCount*2)
			release.Commit.ParentOID = parent.Commit.OID
			release.Commit.TreeOID = fmt.Sprintf("%040x", 980001+eventCount*2)
			release.Tree.OID = release.Commit.TreeOID
			release.Commit.ChangedPaths = stateV3TreeChanges(parent.Tree, release.Tree)
			earlyReleased := StateV3Authentication{StateRef: prior.StateRef, HeadOID: release.Commit.OID, CheckpointOID: prior.CheckpointOID, Current: release, Predecessors: append([]StateV3StateSnapshot{parent}, prior.Predecessors...)}
			fixtureStateV3BindPriorLaneHead(t, policy, earlyReleased, &current)
			later := fixtureStateV3AfterHistory(t, policy, earlyReleased, current, false)
			if err := ValidateStateV3Record(currentRaw, current, policy, later); err == nil {
				t.Fatal("later operation accepted after incomplete prior lease release")
			}
		})
	}

	prior := fixtureStateV3Authentication(t, policy, full)
	fixtureStateV3BindPriorLaneHead(t, policy, prior, &current)
	later := fixtureStateV3AfterHistory(t, policy, prior, current, false)
	opaque := cloneStateV3(t, later)
	for index := range opaque.Predecessors {
		if opaque.Predecessors[index].LeasePresent && opaque.Predecessors[index].ActiveRecord.Present {
			opaque.Predecessors[index].ActiveRecord.Raw = []byte("opaque")
			break
		}
	}
	if err := ValidateStateV3Record(currentRaw, current, policy, opaque); err == nil {
		t.Fatal("opaque foreign active record accepted")
	}

	patch := fixtureStateV3RecordAtEventCount(t, full, 1)
	patch.Reservation.RequestedVersion = "v2.11.1"
	patch.Reservation.GenerationVersion = "v2.11.1-rc.1"
	patch.Reservation.BodySnapshot = "/gardener release:promote v2.11.1"
	patch.Reservation.Marker = Marker("123", "789", "release:promote", "v2.11.1")
	context := Context{RepositoryID: "123", RepositoryFullName: RepositoryFullName, IssueNumber: "456", OriginalCommentID: "789", AcknowledgementCommentID: "790", BodySnapshot: patch.Reservation.BodySnapshot, PolicyRevision: patch.Reservation.PolicyRevision}
	patch.Reservation.RequestSHA256 = RequestSHA256(context, patch.Reservation.Command, patch.Reservation.RequestedVersion)
	patch.Binding.RequestSHA256 = patch.Reservation.RequestSHA256
	fixtureStateV3VersionResolution(t, &patch.Reservation, "v2.11.0-dev")
	rebindStateV3Events(t, &patch)
	patchRaw, _ := canonicalJSON(patch)
	patchAuth := fixtureStateV3Authentication(t, policy, patch)
	patchAuth.StateRef = StateV3MinorStateRef
	patchAuth.CheckpointOID = policy.StateLanes.Minor.CheckpointOID
	patchAuth.Predecessors[len(patchAuth.Predecessors)-1].Commit.OID = policy.StateLanes.Minor.CheckpointOID
	if err := ValidateStateV3Record(patchRaw, patch, policy, patchAuth); err == nil {
		t.Fatal("patch lifecycle accepted on minor lane")
	}
}

func TestStateV3LeaseOnlyRecoveryAndExactRelease(t *testing.T) {
	policy := fixtureStateV3Policy()
	full := fixtureStateV3Record(t)
	reserved := fixtureStateV3RecordAtEventCount(t, full, 1)
	auth := fixtureStateV3Authentication(t, policy, reserved)
	leaseOnly := auth.Predecessors[0]
	leaseCoordination := *auth.Coordination
	leaseAuth := StateV3Authentication{StateRef: auth.StateRef, HeadOID: leaseOnly.Commit.OID, CheckpointOID: auth.CheckpointOID, Current: leaseOnly, Predecessors: auth.Predecessors[1:], Coordination: &leaseCoordination}
	lease := stateV3LeaseForReservation(reserved.Reservation)
	rawLease, _ := canonicalJSON(lease)
	if err := ValidateStateV3ActiveLease(rawLease, lease, reserved.Reservation, policy, leaseAuth); err != nil {
		t.Fatalf("same-operation lease-only recovery rejected: %v", err)
	}
	wrongReservation := reserved.Reservation
	wrongReservation.OriginalCommentID = "999"
	if err := ValidateStateV3ActiveLease(rawLease, lease, wrongReservation, policy, leaseAuth); err == nil {
		t.Fatal("orphan lease was recoverable by a different operation")
	}
	prior := fixtureStateV3Authentication(t, policy, full)
	second := fixtureStateV3ReservedForComment(t, "998")
	second.Reservation.VersionResolution.Coordination = fixtureStateV3ReleasedCoordination(t, full)
	fixtureStateV3CoordinationClaim(t, &second.Reservation)
	fixtureStateV3BindPriorLaneHead(t, policy, prior, &second)
	secondLeaseAuth := fixtureStateV3AfterHistory(t, policy, prior, second, true)
	secondLease := stateV3LeaseForReservation(second.Reservation)
	secondLeaseRaw, _ := canonicalJSON(secondLease)
	if err := ValidateStateV3ActiveLease(secondLeaseRaw, secondLease, second.Reservation, policy, secondLeaseAuth); err != nil {
		t.Fatalf("second lease-only crash after fully released operation rejected: %v", err)
	}

	prepared := fixtureStateV3RecordAtEventCount(t, full, 2)
	preparedRaw, _ := canonicalJSON(prepared)
	earlyRelease := fixtureStateV3Authentication(t, policy, prepared)
	previous := earlyRelease.Current
	current := cloneStateV3(t, previous)
	fixtureStateV3RemoveLease(&current)
	current.Commit.OID = fmt.Sprintf("%040x", 940001)
	current.Commit.ParentOID = previous.Commit.OID
	current.Commit.TreeOID = fmt.Sprintf("%040x", 940002)
	current.Tree.OID = current.Commit.TreeOID
	current.Commit.ChangedPaths = stateV3TreeChanges(previous.Tree, current.Tree)
	earlyRelease.Current = current
	earlyRelease.HeadOID = current.Commit.OID
	earlyRelease.Predecessors = append([]StateV3StateSnapshot{previous}, earlyRelease.Predecessors...)
	if err := ValidateStateV3Record(preparedRaw, prepared, policy, earlyRelease); err == nil {
		t.Fatal("lane lease released before complete")
	}

	completeRaw, _ := canonicalJSON(full)
	released := fixtureStateV3Authentication(t, policy, full)
	if err := ValidateStateV3Record(completeRaw, full, policy, released); err != nil {
		t.Fatalf("exact separate complete lease release rejected: %v", err)
	}
	beforeRelease := cloneStateV3(t, released)
	beforeRelease.Current = beforeRelease.Predecessors[0]
	beforeRelease.HeadOID = beforeRelease.Current.Commit.OID
	beforeRelease.Predecessors = beforeRelease.Predecessors[1:]
	if err := ValidateStateV3Record(completeRaw, full, policy, beforeRelease); err != nil {
		t.Fatalf("complete record awaiting exact lease release rejected: %v", err)
	}
}

func TestStateV3ConservativeCapacityBoundaries(t *testing.T) {
	makeLeaseAuth := func(policy StateV3Policy, record StateV3Record) StateV3Authentication {
		auth := fixtureStateV3Authentication(t, policy, record)
		leaseOnly := auth.Predecessors[len(auth.Predecessors)-2]
		predecessors := auth.Predecessors[len(auth.Predecessors)-1:]
		coordination := *auth.Coordination
		return StateV3Authentication{StateRef: auth.StateRef, HeadOID: leaseOnly.Commit.OID, CheckpointOID: auth.CheckpointOID, Current: leaseOnly, Predecessors: predecessors, Coordination: &coordination}
	}
	check := func(name string, policy StateV3Policy, record StateV3Record, want int) {
		t.Run(name, func(t *testing.T) {
			lane, _ := stateV3LaneForReservation(record.Reservation, policy)
			if lane.MaxHistoryCommits != want {
				t.Fatalf("lane bound = %d, want %d", lane.MaxHistoryCommits, want)
			}
			lease := stateV3LeaseForReservation(record.Reservation)
			rawLease, _ := canonicalJSON(lease)
			leaseAuth := makeLeaseAuth(policy, record)
			if err := ValidateStateV3ActiveLease(rawLease, lease, record.Reservation, policy, leaseAuth); err != nil {
				t.Fatalf("exact-bound lease-only state rejected: %v", err)
			}
			raw, _ := canonicalJSON(record)
			auth := fixtureStateV3Authentication(t, policy, record)
			if err := ValidateStateV3Record(raw, record, policy, auth); err != nil {
				t.Fatalf("exact-bound reserved unstaged state rejected: %v", err)
			}
			short := cloneStateV3(t, policy)
			if lane.StateRef == StateV3MinorStateRef {
				short.StateLanes.Minor.MaxHistoryCommits--
			} else {
				short.StateLanes.Patch.MaxHistoryCommits--
			}
			if err := ValidateStateV3ActiveLease(rawLease, lease, record.Reservation, short, leaseAuth); err == nil {
				t.Fatal("one-short lease-only state accepted")
			}
		})
	}
	prepare := fixtureStateV3RecordAtEventCount(t, fixtureStateV3ArmedSourceBranch(t), 1)
	minorPolicy := fixtureStateV3Policy()
	minorPolicy.StateLanes.Minor.MaxHistoryCommits = 4*MaxStateV3Tags + StateV3PrepareHistoryOverhead
	check("lease and reserved prepare", minorPolicy, prepare, 345)

	patchRecord := func(command string) StateV3Record {
		var record StateV3Record
		source := "v2.11.0-dev"
		if command == "release:release" {
			record = fixtureStateV3RecordAtEventCount(t, fixtureReleaseStateV3Record(t), 1)
			source = "v2.11.1-rc.1"
		} else {
			record = fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
		}
		record.Reservation.Command = command
		record.Reservation.RequestedVersion = "v2.11.1"
		record.Reservation.GenerationVersion = map[string]string{"release:promote": "v2.11.1-rc.1", "release:release": "v2.11.1"}[command]
		record.Reservation.BodySnapshot = "/gardener " + command + " v2.11.1"
		record.Reservation.Marker = Marker("123", "789", command, "v2.11.1")
		context := Context{RepositoryID: "123", RepositoryFullName: RepositoryFullName, IssueNumber: "456", OriginalCommentID: "789", AcknowledgementCommentID: "790", BodySnapshot: record.Reservation.BodySnapshot, PolicyRevision: record.Reservation.PolicyRevision}
		record.Reservation.RequestSHA256 = RequestSHA256(context, command, "v2.11.1")
		record.Binding.RequestSHA256 = record.Reservation.RequestSHA256
		fixtureStateV3VersionResolution(t, &record.Reservation, source)
		rebindStateV3Events(t, &record)
		return record
	}
	patchPolicy := fixtureStateV3Policy()
	patchPolicy.StateLanes.Patch.MaxHistoryCommits = 4*MaxStateV3Tags + StateV3OtherHistoryOverhead
	check("lease and reserved promote", patchPolicy, patchRecord("release:promote"), 339)
	check("lease and reserved release", patchPolicy, patchRecord("release:release"), 339)

	full := fixtureStateV3Record(t)
	reserved := fixtureStateV3RecordAtEventCount(t, full, 1)
	auth := fixtureStateV3Authentication(t, fixtureStateV3Policy(), fixtureStateV3RecordAtEventCount(t, full, 2))
	staged := auth.Predecessors[0]
	remaining, known := stateV3RemainingOperationCommits(reserved, staged)
	stagedDepth := len(auth.Predecessors)
	if !known || !stateV3CapacityFits(stagedDepth, remaining, stagedDepth+remaining) || stateV3CapacityFits(stagedDepth, remaining, stagedDepth+remaining-1) {
		t.Fatal("staged exact/one-short capacity boundary is not closed")
	}
	completeAuth := fixtureStateV3Authentication(t, fixtureStateV3Policy(), full)
	awaitingRelease := completeAuth.Predecessors[0]
	remaining, known = stateV3RemainingOperationCommits(full, awaitingRelease)
	awaitingReleaseDepth := len(completeAuth.Predecessors)
	if !known || remaining != 1 || !stateV3CapacityFits(awaitingReleaseDepth, remaining, awaitingReleaseDepth+remaining) || stateV3CapacityFits(awaitingReleaseDepth, remaining, awaitingReleaseDepth+remaining-1) {
		t.Fatal("complete-awaiting-release capacity boundary is not exact")
	}
}

func TestStateV3HistoryCapacityUsesActualCheckpointDepth(t *testing.T) {
	record := fixtureStateV3ArmedSourceBranch(t)
	policy := fixtureStateV3Policy()
	authentication := fixtureStateV3Authentication(t, policy, record)
	remaining, known := stateV3RemainingOperationCommits(record, authentication.Current)
	if !known {
		t.Fatal("prepared history capacity unexpectedly unknown")
	}
	exact := len(authentication.Predecessors) + 1 + remaining
	minimum := 4*MaxStateV3Tags + StateV3PrepareHistoryOverhead
	if exact > minimum {
		t.Fatalf("fixture exact depth %d exceeds conservative lane minimum %d", exact, minimum)
	}
	policy.StateLanes.Minor.MaxHistoryCommits = minimum
	authentication = fixtureStateV3Authentication(t, policy, record)
	raw, _ := canonicalJSON(record)
	if err := ValidateStateV3Record(raw, record, policy, authentication); err != nil {
		t.Fatalf("remaining-history capacity inside conservative lane bound rejected: %v", err)
	}
}

func TestStateV3RejectsCheckpointAndHistoryAmbiguity(t *testing.T) {
	record := fixtureStateV3Record(t)
	policy := fixtureStateV3Policy()
	base := fixtureStateV3Authentication(t, policy, record)
	for name, mutate := range map[string]func(*StateV3Authentication){
		"wrong state ref":    func(a *StateV3Authentication) { a.StateRef = "refs/heads/caller" },
		"missing head":       func(a *StateV3Authentication) { a.HeadOID = "" },
		"head tree mismatch": func(a *StateV3Authentication) { a.Current.Tree.OID = v3OIDc },
		"record path":        func(a *StateV3Authentication) { a.Current.RecordPath = "requests/123/790/state.json" },
		"unrelated blob": func(a *StateV3Authentication) {
			a.Current.RecordBlobOID = v3OIDa
			for index := range a.Current.Tree.Entries {
				if a.Current.Tree.Entries[index].Path == a.Current.RecordPath {
					a.Current.Tree.Entries[index].OID = v3OIDa
				}
			}
		},
		"altered raw bytes": func(a *StateV3Authentication) { a.Current.RawRecord = append(a.Current.RawRecord, ' ') },
		"extra operation tree entry": func(a *StateV3Authentication) {
			a.Current.Tree.Entries = append(a.Current.Tree.Entries, StateV3StateTreeEntry{Path: "requests/123/789/extra.json", Mode: "100644", Type: "blob", OID: v3OIDa})
			sort.Slice(a.Current.Tree.Entries, func(i, j int) bool { return a.Current.Tree.Entries[i].Path < a.Current.Tree.Entries[j].Path })
		},
		"missing prepared tree entry": func(a *StateV3Authentication) { a.Current.Tree.Entries = a.Current.Tree.Entries[1:] },
		"record digest":               func(a *StateV3Authentication) { a.Current.RecordSHA256 = v3SHAa },
		"head mismatch":               func(a *StateV3Authentication) { a.HeadOID = v3OIDb },
		"self parent":                 func(a *StateV3Authentication) { a.Current.Commit.ParentOID = a.Current.Commit.OID },
		"wrong checkpoint":            func(a *StateV3Authentication) { a.CheckpointOID = v3OIDb },
		"checkpoint not reached":      func(a *StateV3Authentication) { a.Predecessors[len(a.Predecessors)-1].Commit.OID = v3OIDb },
		"zero history":                func(a *StateV3Authentication) { a.Predecessors = nil },
		"history overflow": func(a *StateV3Authentication) {
			entry := a.Predecessors[0]
			a.Predecessors = make([]StateV3StateSnapshot, policy.StateLanes.Minor.MaxHistoryCommits)
			for index := range a.Predecessors {
				a.Predecessors[index] = entry
			}
		},
		"broken ancestry": func(a *StateV3Authentication) { a.Current.Commit.ParentOID = v3OIDc },
		"repeated commit": func(a *StateV3Authentication) {
			a.Predecessors[1].Commit.OID = a.Current.Commit.OID
			a.Predecessors[0].Commit.ParentOID = a.Predecessors[1].Commit.OID
		},
		"unexpected paths": func(a *StateV3Authentication) {
			a.Current.Commit.ChangedPaths = []StateV3ChangedPath{{Path: "caller/state.json", ChildOID: v3OIDa}}
		},
		"truncated tree":        func(a *StateV3Authentication) { a.Current.Tree.Truncated = true },
		"tree entry wrong mode": func(a *StateV3Authentication) { a.Current.Tree.Entries[0].Mode = "100755" },
		"incomplete tree":       func(a *StateV3Authentication) { a.Current.Tree.Complete = false },
		"unverified ancestor":   func(a *StateV3Authentication) { a.Predecessors[0].Commit.RESTVerified = false },
		"wrong state writer":    func(a *StateV3Authentication) { a.Predecessors[0].Commit.Roles.AuthorREST.Login = "caller" },
		"rewritten reservation": func(a *StateV3Authentication) {
			var prior StateV3Record
			if err := json.Unmarshal(a.Predecessors[0].RawRecord, &prior); err != nil {
				t.Fatal(err)
			}
			prior.Reservation.ValidatedActorLogin = "different-actor"
			rebindStateV3Events(t, &prior)
			rewriteStateV3SnapshotRecord(t, &a.Predecessors[0], prior)
		},
		"rewritten event prefix": func(a *StateV3Authentication) {
			var prior StateV3Record
			if err := json.Unmarshal(a.Predecessors[0].RawRecord, &prior); err != nil {
				t.Fatal(err)
			}
			prior.Events[0].EvidenceSHA256 = v3SHAc
			prior.Events = rechainStateV3Events(t, prior.Events)
			rewriteStateV3SnapshotRecord(t, &a.Predecessors[0], prior)
		},
	} {
		t.Run(name, func(t *testing.T) {
			authentication := cloneStateV3(t, base)
			mutate(&authentication)
			raw, _ := canonicalJSON(record)
			if err := ValidateStateV3Record(raw, record, policy, authentication); err == nil {
				t.Fatal("ambiguous state history accepted")
			}
		})
	}
	raw, err := canonicalJSON(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateStateV3Record(raw, record, policy, base); err != nil {
		t.Fatalf("exact valid child history rejected: %v", err)
	}
	if err := ValidateStateV3Record(append(append([]byte(nil), raw...), ' '), record, policy, base); err == nil {
		t.Fatal("altered caller-supplied raw record bytes accepted")
	}
}
