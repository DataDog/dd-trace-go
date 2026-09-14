// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"strings"
	"testing"
)

func TestPreparePendingReservationResolvesOneNextDevelopmentSignedOutput(t *testing.T) {
	source := strings.Repeat("a", 40)
	release := strings.Repeat("b", 40)
	reservation := baseReservation()
	reservation.Command = "release:prepare"
	reservation.RequestedVersion = "v2.9.0"
	reservation.BodySnapshot = "/gardener release:prepare v2.9.0"
	reservation.ResolvedVersion = "v2.9.0"
	reservation.DevelopmentVersion = "v2.10.0-dev"
	reservation.GenerationVersion = reservation.DevelopmentVersion
	reservation.ReleaseLine = "v2.9"
	reservation.SourceRefs = []SourceRef{{Ref: "refs/heads/main", SHA: source}}
	reservation.BranchIntents = []BranchIntent{
		{Ref: "refs/heads/release-v2.9.x", DesiredSHA: "pending"},
		{Ref: "refs/heads/dev-v2.10.x", DesiredSHA: "pending"},
	}
	reservation.RequestSHA256 = RequestSHA256(Context{RepositoryID: reservation.RepositoryID, RepositoryFullName: reservation.RepositoryFullName, IssueNumber: reservation.IssueNumber, OriginalCommentID: reservation.OriginalCommentID, AcknowledgementCommentID: reservation.AcknowledgementCommentID, BodySnapshot: reservation.BodySnapshot, PolicyRevision: reservation.PolicyRevision}, reservation.Command, reservation.RequestedVersion)
	decision, err := ReserveOperation(nil, reservation, nil)
	if err != nil {
		t.Fatal(err)
	}
	intent := GitSigningIntent{Timestamp: 1, Message: "release: " + reservation.GenerationVersion, Principal: "release@example.com", Fingerprint: "SHA256:test"}
	recorded, err := RecordGitSigningIntent(decision.Record, intent)
	if err != nil {
		t.Fatal(err)
	}
	signed := SignedOutput{UnsignedSHA: strings.Repeat("c", 40), SourceSHA: source, TreeSHA: strings.Repeat("d", 40), ReleaseSHA: release, ToolDigest: strings.Repeat("a", 64), ValidatorDigest: strings.Repeat("b", 64), CommitParentSHA: source, SignerFingerprint: strings.Repeat("e", 64), Tags: []TagRef{{Name: "v2.10.0-dev", Ref: "refs/tags/v2.10.0-dev", TagObjectSHA: strings.Repeat("f", 40), PeeledCommitSHA: release}}, Bundle: Bundle{Path: "requests/123/789/recovery.bundle", SHA256: strings.Repeat("1", 64), SizeBytes: 1, PrerequisiteSHAs: []string{source}}}
	next, already, err := AdvanceToSigned(recorded, signed, signedRecordEvidence(t, signed))
	if err != nil || already {
		t.Fatalf("AdvanceToSigned: already=%v err=%v", already, err)
	}
	want := []BranchIntent{{Ref: "refs/heads/release-v2.9.x", DesiredSHA: source}, {Ref: "refs/heads/dev-v2.10.x", DesiredSHA: release}}
	if next.SignedOutput == nil || !equalBranchIntents(next.SignedOutput.PublicationIntents, want) {
		t.Fatalf("publication intents = %#v", next.SignedOutput)
	}
	probe := next
	probe.Phase = PhaseBranchesPublished
	targets, err := requiredTestTargets(probe)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0] != (testTarget{Branch: "release-v2.9.x", SHA: source}) || targets[1] != (testTarget{Branch: "dev-v2.10.x", SHA: release}) {
		t.Fatalf("targets = %#v", targets)
	}
	changed := signed
	changed.PublicationIntents = []BranchIntent{{Ref: "refs/heads/release-v2.9.x", DesiredSHA: release}, {Ref: "refs/heads/dev-v2.10.x", DesiredSHA: release}}
	if _, _, err := AdvanceToSigned(next, changed, signedRecordEvidence(t, changed)); err == nil {
		t.Fatal("changed resolved publication intent accepted")
	}
}

func TestPendingReservationCannotPublish(t *testing.T) {
	record := Record{Reservation: baseReservation(), Phase: PhaseReserved}
	if _, err := PublishBranches(t.Context(), ExecRunner{}, VerifiedPublication{record: record, workDir: t.TempDir()}, t.TempDir()); err == nil {
		t.Fatal("pending reservation published")
	}
}
