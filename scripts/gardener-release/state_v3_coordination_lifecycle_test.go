// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

func fixtureStateV3LaneTermination(t *testing.T, auth StateV3Authentication, claim StateV3ReleaseLineClaimEvidence) StateV3LaneTerminationEvidence {
	t.Helper()
	before := auth.Predecessors[0]
	return StateV3LaneTerminationEvidence{StateRef: auth.StateRef, CheckpointOID: auth.CheckpointOID, CompleteRecordOID: before.Commit.OID, CompleteRecordSHA256: before.RecordSHA256, CompleteRecordBlobOID: before.RecordBlobOID, LeaseReleaseHeadOID: auth.HeadOID, ObservedRefOID: auth.HeadOID, CoordinationClaimOID: claim.Commit.OID, CoordinationClaimBlobOID: claim.BlobOID, CoordinationClaimSHA256: claim.SHA256}
}

func fixtureStateV3ReleasedCoordination(t *testing.T, record StateV3Record) StateV3CoordinationObservation {
	t.Helper()
	policy := fixtureStateV3Policy()
	laneAuth := fixtureStateV3Authentication(t, policy, record)
	claim := record.Reservation.CoordinationClaim
	base := record.Reservation.VersionResolution.Coordination.Authentication
	acquired := StateV3CoordinationSnapshot{Commit: claim.Commit, Tree: claim.Tree, Claims: []StateV3ReleaseLineClaimEvidence{claim}}
	releaseArm := fixtureStateV3CoordinationArm(t, acquired, claim.Path, "claim_release", claim.BlobOID, claim.Claim, 20, policy)
	releasedCommit := StateV3StateCommitEvidence{OID: fmt.Sprintf("%040x", 990001), ParentOID: releaseArm.Commit.OID, TreeOID: fmt.Sprintf("%040x", 990002), RESTVerified: true, RESTReason: StateV3RequiredRESTVerificationReason, GraphQLSignatureValid: true, WasSignedByGitHub: true, SignatureState: StateV3RequiredSignatureState, Roles: policy.CommitRoles}
	released := StateV3CoordinationSnapshot{Commit: releasedCommit, Tree: StateV3StateTreeEvidence{OID: releasedCommit.TreeOID, Complete: true, Entries: []StateV3StateTreeEntry{}}, Claims: []StateV3ReleaseLineClaimEvidence{}}
	released.Commit.ChangedPaths = stateV3TreeChanges(releaseArm.Tree, released.Tree)
	released.Release = &StateV3CoordinationReleaseEvidence{Path: claim.Path, ClaimSHA256: claim.SHA256, ClaimBlobOID: claim.BlobOID, ClaimCommitOID: claim.Commit.OID, Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: released.Commit.OID}, ObservedRefOID: released.Commit.OID, LaneTermination: fixtureStateV3LaneTermination(t, laneAuth, claim)}
	auth := StateV3CoordinationAuthentication{StateRef: policy.Coordination.StateRef, CheckpointOID: policy.Coordination.CheckpointOID, HeadOID: released.Commit.OID, Current: released, Predecessors: append([]StateV3CoordinationSnapshot{releaseArm, acquired, base.Current}, base.Predecessors...), LaneTerminations: []StateV3Authentication{laneAuth}}
	return StateV3CoordinationObservation{StateRef: policy.Coordination.StateRef, CheckpointOID: policy.Coordination.CheckpointOID, Head: released.Commit, Tree: released.Tree, Claims: released.Claims, Authentication: auth}
}

func TestStateV3CoordinationAuthenticatesClaimLifecycleAndReacquire(t *testing.T) {
	policy := fixtureStateV3Policy()
	completed := fixtureStateV3Record(t)
	observation := fixtureStateV3ReleasedCoordination(t, completed)
	if !validStateV3CoordinationAuthentication(observation.Authentication, policy) {
		t.Fatal("exact complete lane then lease then claim release lifecycle rejected")
	}

	reacquire := fixtureStateV3ReservedForComment(t, "998")
	reacquire.Reservation.VersionResolution.Coordination = observation
	fixtureStateV3CoordinationClaim(t, &reacquire.Reservation)
	rebindStateV3Events(t, &reacquire)
	if err := validateV3Fixture(t, reacquire); err != nil {
		t.Fatalf("same-line reacquire after exact coordination release rejected: %v", err)
	}

	for name, mutate := range map[string]func(*StateV3CoordinationSnapshot){
		"early deletion": func(snapshot *StateV3CoordinationSnapshot) {
			snapshot.Release.LaneTermination.CompleteRecordOID = v3OIDd
		},
		"terminal arm deletion": func(snapshot *StateV3CoordinationSnapshot) {
			snapshot.Release.LaneTermination.LeaseReleaseHeadOID = v3OIDd
		},
		"wrong lane termination": func(snapshot *StateV3CoordinationSnapshot) {
			snapshot.Release.LaneTermination.StateRef = StateV3PatchStateRef
		},
		"lost release mismatch": func(snapshot *StateV3CoordinationSnapshot) {
			snapshot.Release.Response = StateV3MutationResponse{Observation: "lost", Attempts: 1}
			snapshot.Release.ObservedRefOID = v3OIDd
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneStateV3(t, observation.Authentication)
			mutate(&candidate.Current)
			if validStateV3CoordinationAuthentication(candidate, policy) {
				t.Fatal("invalid claim release lifecycle accepted")
			}
		})
	}
}

func TestStateV3CoordinationCompleteTreeProjectionAndCAS(t *testing.T) {
	record := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	foreign := fixtureStateV3PatchOperation(t, "v2.10", "v2.10.1")
	fixtureStateV3SetCoordinationClaims(t, &record.Reservation.VersionResolution, []StateV3ReleaseLineClaimEvidence{foreign.Reservation.CoordinationClaim})
	record.Reservation.VersionResolution.ExistingOperations = []ExistingOperation{{RequestKey: foreign.Reservation.RequestKey, RequestSHA256: foreign.Reservation.RequestSHA256, Command: foreign.Reservation.Command, ReleaseLine: foreign.Reservation.ReleaseLine, ResolvedVersion: foreign.Reservation.ResolvedVersion, DevelopmentVersion: foreign.Reservation.DevelopmentVersion, Phase: string(foreign.Phase)}}
	fixtureStateV3CoordinationClaim(t, &record.Reservation)
	rebindStateV3Events(t, &record)
	if err := validateV3Fixture(t, record); err != nil {
		t.Fatalf("valid different-line sequential claim rejected: %v", err)
	}

	for name, mutate := range map[string]func(*StateV3Record){
		"omitted claim": func(candidate *StateV3Record) {
			candidate.Reservation.VersionResolution.Coordination.Authentication.Current.Claims = []StateV3ReleaseLineClaimEvidence{}
			candidate.Reservation.VersionResolution.Coordination.Claims = []StateV3ReleaseLineClaimEvidence{}
		},
		"unprojected tree claim": func(candidate *StateV3Record) {
			current := &candidate.Reservation.VersionResolution.Coordination.Authentication.Current
			current.Tree.Entries = append(current.Tree.Entries, StateV3StateTreeEntry{Path: "release-lines/2.9.json", Mode: "100644", Type: "blob", OID: v3OIDd})
			candidate.Reservation.VersionResolution.Coordination.Tree = current.Tree
		},
		"overwrite same line": func(candidate *StateV3Record) {
			path := candidate.Reservation.CoordinationClaim.Path
			parent := &candidate.Reservation.VersionResolution.Coordination.Authentication.Current
			parent.Claims = append(parent.Claims, candidate.Reservation.CoordinationClaim)
			parent.Tree.Entries = append(parent.Tree.Entries, StateV3StateTreeEntry{Path: path, Mode: "100644", Type: "blob", OID: candidate.Reservation.CoordinationClaim.BlobOID})
			candidate.Reservation.VersionResolution.Coordination.Claims = parent.Claims
			candidate.Reservation.VersionResolution.Coordination.Tree = parent.Tree
		},
		"collateral acquire change": func(candidate *StateV3Record) {
			candidate.Reservation.CoordinationClaim.Tree.Entries = append(candidate.Reservation.CoordinationClaim.Tree.Entries, StateV3StateTreeEntry{Path: "release-lines/2.9.json", Mode: "100644", Type: "blob", OID: v3OIDd})
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneStateV3(t, record)
			mutate(&candidate)
			rebindStateV3Events(t, &candidate)
			if err := validateV3Fixture(t, candidate); err == nil {
				t.Fatal("invalid coordination projection or CAS accepted")
			}
		})
	}
}

func TestStateV3CoordinationReleaseOptionalEvidenceFailsClosed(t *testing.T) {
	policy := fixtureStateV3Policy()
	completed := fixtureStateV3Record(t)
	observation := fixtureStateV3ReleasedCoordination(t, completed)
	record := fixtureStateV3ReservedForComment(t, "998")
	record.Reservation.VersionResolution.Coordination = observation
	fixtureStateV3CoordinationClaim(t, &record.Reservation)
	rebindStateV3Events(t, &record)
	raw, err := canonicalJSON(record)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*StateV3CoordinationAuthentication){
		"nil release":         func(authentication *StateV3CoordinationAuthentication) { authentication.Predecessors[0].Release = nil },
		"nil current arm":     func(authentication *StateV3CoordinationAuthentication) { authentication.Current.Arm = nil },
		"missing termination": func(authentication *StateV3CoordinationAuthentication) { authentication.LaneTerminations = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			authentication := fixtureStateV3Authentication(t, policy, record)
			coordination := *authentication.Coordination
			mutate(&coordination)
			authentication.Coordination = &coordination
			defer func() {
				if recover() != nil {
					t.Fatal("malformed coordination evidence panicked")
				}
			}()
			if err := ValidateStateV3Record(raw, record, policy, authentication); err == nil {
				t.Fatal("malformed optional coordination evidence accepted")
			}
		})
	}
}

func TestStateV3CoordinationArmsBindAttemptAndExactClaim(t *testing.T) {
	record := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	for name, mutate := range map[string]func(*StateV3Record){
		"acquisition zero-attempt result": func(candidate *StateV3Record) {
			candidate.Reservation.CoordinationClaim.Acquired = StateV3MutationResponse{Observation: "not_attempted"}
		},
		"acquisition missing intended claim": func(candidate *StateV3Record) {
			candidate.Reservation.VersionResolution.Coordination.Authentication.Current.Arm.Arm.IntendedClaimSHA256 = ""
		},
		"acquisition mismatched intended claim": func(candidate *StateV3Record) {
			candidate.Reservation.VersionResolution.Coordination.Authentication.Current.Arm.Arm.IntendedClaimSHA256 = v3SHAa
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneStateV3(t, record)
			candidate.Reservation.VersionResolution.Coordination.Authentication = cloneStateV3(t, candidate.Reservation.VersionResolution.Coordination.Authentication)
			mutate(&candidate)
			refreshStateV3CoordinationAcquireArm(t, &candidate)
			raw, err := canonicalJSON(candidate)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if recover() != nil {
					t.Fatal("malformed coordination acquisition panicked")
				}
			}()
			if err := ValidateStateV3Record(raw, candidate, fixtureStateV3Policy(), fixtureStateV3Authentication(t, fixtureStateV3Policy(), candidate)); err == nil {
				t.Fatal("invalid coordination acquisition accepted")
			}
		})
	}

	completed := fixtureStateV3Record(t)
	observation := fixtureStateV3ReleasedCoordination(t, completed)
	observation.Authentication.Current.Release.Response = StateV3MutationResponse{Observation: "not_attempted"}
	if validStateV3CoordinationAuthentication(observation.Authentication, fixtureStateV3Policy()) {
		t.Fatal("coordination release consumed an arm with a zero-attempt result")
	}
}

func refreshStateV3CoordinationAcquireArm(t *testing.T, record *StateV3Record) {
	t.Helper()
	coordination := &record.Reservation.VersionResolution.Coordination
	authentication := &coordination.Authentication
	if authentication.Current.Arm == nil || authentication.Current.Arm.Arm.Operation != "claim_acquire" || len(authentication.Predecessors) == 0 {
		t.Fatal("fixture coordination acquisition arm unavailable")
	}
	arm := authentication.Current.Arm
	raw, err := canonicalJSON(arm.Arm)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	arm.Raw = raw
	arm.SHA256 = hex.EncodeToString(digest[:])
	arm.BlobOID = stateV3GitBlobOID(raw)
	for index := range authentication.Current.Tree.Entries {
		if authentication.Current.Tree.Entries[index].Path == stateV3CoordinationArmPath {
			authentication.Current.Tree.Entries[index].OID = arm.BlobOID
		}
	}
	authentication.Current.Commit.ChangedPaths = stateV3TreeChanges(authentication.Predecessors[0].Tree, authentication.Current.Tree)
	record.Reservation.CoordinationClaim.Commit.ChangedPaths = stateV3TreeChanges(authentication.Current.Tree, record.Reservation.CoordinationClaim.Tree)
	coordination.Head = authentication.Current.Commit
	coordination.Tree = authentication.Current.Tree
	coordination.Claims = authentication.Current.Claims
}

func TestStateV3SourceProvenanceDoesNotRequireStateWriterRoles(t *testing.T) {
	record := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	source := record.Reservation.VersionResolution.Source
	// Existing source and historical tag targets may predate GitHub-created
	// state commits. Exact ref/commit/tree/blob linkage remains mandatory.
	source.Commit.RESTVerified = false
	source.Commit.RESTReason = "unsigned"
	source.Commit.GraphQLSignatureValid = false
	source.Commit.WasSignedByGitHub = false
	source.Commit.SignatureState = "NONE"
	source.Commit.Roles = StateV3CommitRoles{}
	if !validStateV3SourceVersion(source, source.Ref, source.OID, fixtureStateV3Policy()) {
		t.Fatal("ordinary source commit provenance rejected")
	}
	source.Tree.Entries[0].OID = v3OIDd
	if validStateV3SourceVersion(source, source.Ref, source.OID, fixtureStateV3Policy()) {
		t.Fatal("source tree/blob mismatch accepted")
	}
}

func TestStateV3CoordinationAuthenticationIsTransientAndCapacityIsReserved(t *testing.T) {
	record := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	baseline, err := canonicalJSON(record)
	if err != nil {
		t.Fatal(err)
	}
	// A maximum-depth external chain must not change persisted state bytes.
	transient := record.Reservation.VersionResolution.Coordination.Authentication
	for len(transient.Predecessors)+1 < MaxStateV3HistoryCommits {
		transient.Predecessors = append(transient.Predecessors, transient.Predecessors[len(transient.Predecessors)-1])
	}
	record.Reservation.VersionResolution.Coordination.Authentication = transient
	again, err := canonicalJSON(record)
	if err != nil {
		t.Fatal(err)
	}
	if string(baseline) != string(again) || len(again) > MaxStateV3DocumentBytes {
		t.Fatal("transient coordination history changed bounded record bytes")
	}
	for _, tc := range []struct{ depth, active int }{{1, 0}, {8, 1}, {32, 2}} {
		max := tc.depth + tc.active + 2
		if !stateV3CapacityFits(tc.depth, tc.active+2, max) || stateV3CapacityFits(tc.depth, tc.active+2, max-1) {
			t.Fatalf("coordination acquisition capacity boundary failed: %+v", tc)
		}
	}
}
