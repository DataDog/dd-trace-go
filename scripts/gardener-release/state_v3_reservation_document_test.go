// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"strconv"
	"testing"
)

func TestValidateStateV3ReservationDocumentPortableIntegers(t *testing.T) {
	t.Parallel()

	type target struct {
		name string
		set  func(*StateV3Reservation, int64)
	}
	targets := []target{
		{
			name: "top-level coordination claim attempt",
			set: func(reservation *StateV3Reservation, value int64) {
				reservation.CoordinationClaim.Claim.Attempt = int(value)
			},
		},
		{
			name: "top-level coordination claim acquired attempts",
			set: func(reservation *StateV3Reservation, value int64) {
				reservation.CoordinationClaim.Acquired.Attempts = int(value)
			},
		},
		{
			name: "version resolution coordination claim attempt",
			set: func(reservation *StateV3Reservation, value int64) {
				reservation.VersionResolution.Coordination.Claims[0].Claim.Attempt = int(value)
			},
		},
		{
			name: "version resolution coordination claim acquired attempts",
			set: func(reservation *StateV3Reservation, value int64) {
				reservation.VersionResolution.Coordination.Claims[0].Acquired.Attempts = int(value)
			},
		},
		{
			name: "staged prepared addition count",
			set: func(reservation *StateV3Reservation, value int64) {
				reservation.VersionResolution.LaneHeads[0].Snapshot.StagedEnvelope.Prepared.AdditionCount = int(value)
			},
		},
		{
			name: "staged prepared decoded addition bytes",
			set: func(reservation *StateV3Reservation, value int64) {
				reservation.VersionResolution.LaneHeads[0].Snapshot.StagedEnvelope.Prepared.TotalDecodedAdditionBytes = value
			},
		},
		{
			name: "staged prepared bundle size",
			set: func(reservation *StateV3Reservation, value int64) {
				reservation.VersionResolution.LaneHeads[0].Snapshot.StagedEnvelope.Prepared.Bundle.SizeBytes = value
			},
		},
		{
			name: "staged prepared file size",
			set: func(reservation *StateV3Reservation, value int64) {
				reservation.VersionResolution.LaneHeads[0].Snapshot.StagedEnvelope.Prepared.StateFiles[0].SizeBytes = value
			},
		},
		{
			name: "staged prepared mutation file change size",
			set: func(reservation *StateV3Reservation, value int64) {
				reservation.VersionResolution.LaneHeads[0].Snapshot.StagedEnvelope.Prepared.Mutation.FileChanges[0].SizeBytes = value
			},
		},
		{
			name: "staged file size",
			set: func(reservation *StateV3Reservation, value int64) {
				reservation.VersionResolution.LaneHeads[0].Snapshot.StagedEnvelope.Files[0].SizeBytes = value
			},
		},
		{
			name: "active record staged mutation file change size",
			set: func(reservation *StateV3Reservation, value int64) {
				reservation.VersionResolution.LaneHeads[0].Snapshot.ActiveRecord.StagedEnvelope.Prepared.Mutation.FileChanges[0].SizeBytes = value
			},
		},
	}

	for _, target := range targets {
		t.Run(target.name, func(t *testing.T) {
			for _, value := range []int64{stateV3PortableIntMin, stateV3PortableIntMax} {
				reservation := fixtureStateV3PortableIntegerReservation(t)
				target.set(&reservation, value)
				raw := mustStateV3ReservationDocument(t, reservation)
				if !ValidateStateV3ReservationDocument(raw) {
					t.Fatalf("rejected portable value %d", value)
				}
			}

			for _, values := range [][2]string{
				{strconv.FormatInt(stateV3PortableIntMin, 10), strconv.FormatInt(stateV3PortableIntMin-1, 10)},
				{strconv.FormatInt(stateV3PortableIntMax, 10), strconv.FormatInt(stateV3PortableIntMax+1, 10)},
			} {
				reservation := fixtureStateV3PortableIntegerReservation(t)
				value, err := strconv.ParseInt(values[0], 10, 64)
				if err != nil {
					t.Fatal(err)
				}
				target.set(&reservation, value)
				raw := mustStateV3ReservationDocument(t, reservation)
				overflow := bytes.Replace(raw, []byte(values[0]), []byte(values[1]), 1)
				if bytes.Equal(overflow, raw) {
					t.Fatalf("could not replace %q", values[0])
				}
				if ValidateStateV3ReservationDocument(overflow) {
					t.Fatalf("accepted out-of-range value %s", values[1])
				}
			}
		})
	}
}

func TestValidateStateV3ReservationDocumentPortableIntegersKeepsCanonicalBehavior(t *testing.T) {
	t.Parallel()

	reservation := fixtureStateV3PortableIntegerReservation(t)
	raw := mustStateV3ReservationDocument(t, reservation)
	if !ValidateStateV3ReservationDocument(raw) {
		t.Fatal("rejected canonical reservation with ordinary integers")
	}
	paths, ok := stateV3PreparedPaths(reservation.RepositoryID, reservation.OriginalCommentID)
	if !ok || !ValidateStateV3ReservationDocumentPath(raw, paths.Reservation) {
		t.Fatal("rejected canonical reservation document path")
	}
	if ValidateStateV3ReservationDocument(append(append([]byte(nil), raw...), ' ')) {
		t.Fatal("accepted noncanonical reservation")
	}
}

func fixtureStateV3PortableIntegerReservation(t *testing.T) StateV3Reservation {
	t.Helper()

	reservation := fixtureStateV3Record(t).Reservation
	reservation.VersionResolution.Coordination.Claims = []StateV3ReleaseLineClaimEvidence{reservation.CoordinationClaim}
	reservation.VersionResolution.LaneHeads = []StateV3LaneHeadObservation{{
		Snapshot: StateV3StateSnapshot{
			Commit:         StateV3StateCommitEvidence{ChangedPaths: []StateV3ChangedPath{}},
			Tree:           StateV3StateTreeEvidence{Entries: []StateV3StateTreeEntry{}},
			StagedEnvelope: fixtureStateV3PortableIntegerStagedEnvelope(),
			ActiveRecord: StateV3ActiveRecordEvidence{
				StagedEnvelope: fixtureStateV3PortableIntegerStagedEnvelope(),
			},
		},
	}}
	return reservation
}

func fixtureStateV3PortableIntegerStagedEnvelope() *StateV3StagedEnvelopeEvidence {
	return &StateV3StagedEnvelopeEvidence{
		Prepared: StateV3PreparedState{
			Bundle:     StateV3BundleIdentity{},
			StateFiles: []StateV3PreparedFile{{}},
			Mutation: StateV3CommitMutationIntent{
				Branches:    []StateV3BranchMutationIntent{},
				FileChanges: []StateV3FileChange{{}},
			},
		},
		TagPlans: []StateV3TagPlan{},
		Files:    []StateV3StagedFile{{Raw: []byte{}}},
	}
}

func mustStateV3ReservationDocument(t *testing.T, reservation StateV3Reservation) []byte {
	t.Helper()
	raw, err := canonicalJSON(reservation)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > MaxStateV3ReservationBytes {
		t.Fatalf("reservation document size %d exceeds maximum %d", len(raw), MaxStateV3ReservationBytes)
	}
	return raw
}
