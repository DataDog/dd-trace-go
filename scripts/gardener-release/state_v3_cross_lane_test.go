// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestStateV3SourceAndHistoricalPlanEvidenceAreBound(t *testing.T) {
	record := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	candidate := cloneStateV3(t, record)
	alternate := []byte("package version\n\nvar Tag = \"v2.11.1-dev\"\n")
	digest := sha256.Sum256(alternate)
	candidate.Reservation.VersionResolution.Source.Raw = alternate
	candidate.Reservation.VersionResolution.Source.SHA256 = hex.EncodeToString(digest[:])
	candidate.Reservation.VersionResolution.Source.BlobOID = stateV3GitBlobOID(alternate)
	candidate.Reservation.VersionResolution.Source.Version = "v2.11.1-dev"
	rebindStateV3Events(t, &candidate)
	if err := validateV3Fixture(t, candidate); err == nil {
		t.Fatal("alternate source blob not present in the stated source tree accepted")
	}

	candidate = cloneStateV3(t, record)
	ref := "refs/tags/v2.10.0"
	candidate.Reservation.VersionResolution.RemoteRefs.Tags = []StateV3RefObservation{{Ref: ref, OID: v3OIDd}}
	derivation := fixtureStateV3IncompleteTagDerivation(t, candidate.Reservation, "v2.10.0", v3OIDd, []string{ref})
	// The authenticated plan still requires the module tag; only the asserted
	// expected list is laundered down to the observed root tag.
	derivation.ExpectedTagRefs = []string{ref}
	candidate.Reservation.VersionResolution.RemoteRefs.IncompleteDerivations = []StateV3IncompleteTagDerivation{derivation}
	candidate.Reservation.VersionResolution.RemoteRefs.IncompleteTagVersions = nil
	rebindStateV3Events(t, &candidate)
	if err := validateV3Fixture(t, candidate); err == nil {
		t.Fatal("root-only historical tag derivation laundering accepted")
	}
}

func fixtureStateV3PatchOperation(t *testing.T, line, version string) StateV3Record {
	t.Helper()
	record := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	record.Reservation.OriginalCommentID = "998"
	record.Reservation.RequestKey = record.Reservation.RepositoryID + ":998"
	record.Binding.RequestKey = record.Reservation.RequestKey
	record.Reservation.ReleaseLine = line
	record.Reservation.RequestedVersion = version
	record.Reservation.GenerationVersion = ""
	record.Reservation.ResolvedVersion = ""
	record.Reservation.SourceRef = "refs/heads/release-" + line + ".x"
	record.Reservation.BodySnapshot = "/gardener release:promote " + version
	record.Reservation.Marker = Marker(record.Reservation.RepositoryID, record.Reservation.OriginalCommentID, record.Reservation.Command, version)
	context := Context{RepositoryID: record.Reservation.RepositoryID, RepositoryFullName: record.Reservation.RepositoryFullName, IssueNumber: record.Reservation.IssueNumber, OriginalCommentID: record.Reservation.OriginalCommentID, AcknowledgementCommentID: record.Reservation.AcknowledgementCommentID, BodySnapshot: record.Reservation.BodySnapshot, PolicyRevision: record.Reservation.PolicyRevision}
	record.Reservation.RequestSHA256 = RequestSHA256(context, record.Reservation.Command, version)
	record.Binding.RequestSHA256 = record.Reservation.RequestSHA256
	fixtureStateV3VersionResolution(t, &record.Reservation, line+".0-dev")
	record.Reservation.GenerationVersion = record.Reservation.ResolvedVersion
	rebindStateV3Events(t, &record)
	return record
}

func TestStateV3CrossLaneViewDerivesCompleteExistingOperations(t *testing.T) {
	minor := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	patch := fixtureStateV3PatchOperation(t, "v2.10", "v2.10.1")
	fixtureStateV3SetCoordinationClaims(t, &minor.Reservation.VersionResolution, []StateV3ReleaseLineClaimEvidence{cloneStateV3(t, patch.Reservation.CoordinationClaim)})
	minor.Reservation.VersionResolution.ExistingOperations = []ExistingOperation{{RequestKey: patch.Reservation.RequestKey, RequestSHA256: patch.Reservation.RequestSHA256, Command: patch.Reservation.Command, ReleaseLine: patch.Reservation.ReleaseLine, ResolvedVersion: patch.Reservation.ResolvedVersion, DevelopmentVersion: patch.Reservation.DevelopmentVersion, Phase: string(patch.Phase)}}
	fixtureStateV3CoordinationClaim(t, &minor.Reservation)
	rebindStateV3Events(t, &minor)
	if err := validateV3Fixture(t, minor); err != nil {
		t.Fatalf("different-line minor and patch operations rejected: %v", err)
	}

	omitted := cloneStateV3(t, minor)
	omitted.Reservation.VersionResolution.ExistingOperations = []ExistingOperation{}
	rebindStateV3Events(t, &omitted)
	if err := validateV3Fixture(t, omitted); err == nil {
		t.Fatal("omitted opposite-lane active operation accepted")
	}

	wrongHead := cloneStateV3(t, minor)
	wrongHead.Reservation.VersionResolution.Coordination.Claims[0].ObservedRefOID = v3OIDd
	rebindStateV3Events(t, &wrongHead)
	if err := validateV3Fixture(t, wrongHead); err == nil {
		t.Fatal("mismatched coordination claim observation accepted")
	}

	sameLine := fixtureStateV3PatchOperation(t, "v2.11", "v2.11.1")
	blocked := fixtureStateV3RecordAtEventCount(t, fixtureStateV3Record(t), 1)
	fixtureStateV3SetCoordinationClaims(t, &blocked.Reservation.VersionResolution, []StateV3ReleaseLineClaimEvidence{cloneStateV3(t, sameLine.Reservation.CoordinationClaim)})
	blocked.Reservation.VersionResolution.ExistingOperations = []ExistingOperation{{RequestKey: sameLine.Reservation.RequestKey, RequestSHA256: sameLine.Reservation.RequestSHA256, Command: sameLine.Reservation.Command, ReleaseLine: sameLine.Reservation.ReleaseLine, ResolvedVersion: sameLine.Reservation.ResolvedVersion, DevelopmentVersion: sameLine.Reservation.DevelopmentVersion, Phase: string(sameLine.Phase)}}
	fixtureStateV3CoordinationClaim(t, &blocked.Reservation)
	rebindStateV3Events(t, &blocked)
	if err := validateV3Fixture(t, blocked); err == nil {
		t.Fatal("same-release-line minor and patch operations accepted")
	}
}
