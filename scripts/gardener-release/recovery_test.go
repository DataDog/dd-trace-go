// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// reservationForSigned builds a Reservation whose ReleaseLine/
// ResolvedVersion match a bundleFixture's generated version, so a
// realistic Record can progress through PhaseReserved -> PhaseSigned in
// these tests. It reuses baseReservation()'s other fields for
// consistency with state_test.go/state_git_test.go's fixtures.
func reservationForSigned(generationVersion, sourceSHA string) Reservation {
	reservation := baseReservation()
	version, err := ParseReleaseVersion(generationVersion)
	if err != nil {
		panic(err)
	}
	reservation.GenerationVersion = generationVersion
	switch version.Prerelease {
	case prereleaseDev:
		reservation.Command = "release:prepare"
		reservation.RequestedVersion = "auto"
		reservation.BodySnapshot = "/gardener release:prepare"
		reservation.ReleaseLine = fmt.Sprintf("v%d.%d", version.Major, version.Minor-1)
		reservation.ResolvedVersion = releaseVersion{Major: version.Major, Minor: version.Minor - 1, Patch: 0}.String()
		reservation.DevelopmentVersion = generationVersion
		reservation.SourceRefs = []SourceRef{{Ref: "refs/heads/main", SHA: sourceSHA}}
		reservation.BranchIntents = []BranchIntent{{Ref: "refs/heads/" + releaseBranchName(version.Major, version.Minor-1), DesiredSHA: "pending"}, {Ref: "refs/heads/" + devBranchName(version.Major, version.Minor), DesiredSHA: "pending"}}
	case prereleaseRC:
		reservation.ResolvedVersion = generationVersion
		reservation.ReleaseLine = "v2.9"
		reservation.SourceRefs = []SourceRef{{Ref: "refs/heads/release-v2.9.x", SHA: sourceSHA}}
		reservation.BranchIntents = []BranchIntent{{Ref: "refs/heads/release-v2.9.x", ExpectedOldSHA: sourceSHA, DesiredSHA: "pending"}}
	default:
		reservation.Command = "release:release"
		reservation.ResolvedVersion = generationVersion
		reservation.ReleaseLine = "v2.9"
		reservation.SourceRefs = []SourceRef{{Ref: "refs/heads/release-v2.9.x", SHA: sourceSHA}}
		reservation.BranchIntents = []BranchIntent{{Ref: "refs/heads/release-v2.9.x", ExpectedOldSHA: sourceSHA, DesiredSHA: "pending"}}
	}
	reservation.RequestSHA256 = RequestSHA256(Context{RepositoryID: reservation.RepositoryID, RepositoryFullName: reservation.RepositoryFullName, IssueNumber: reservation.IssueNumber, OriginalCommentID: reservation.OriginalCommentID, AcknowledgementCommentID: reservation.AcknowledgementCommentID, BodySnapshot: reservation.BodySnapshot, PolicyRevision: reservation.PolicyRevision}, reservation.Command, reservation.RequestedVersion)
	return reservation
}

// signedRecordEvidence is the strict, bounded EventPhaseAdvanced payload
// required for the signed transition.
func signedRecordEvidence(t *testing.T, signed SignedOutput) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(signedPhaseEvidence{Phase: PhaseSigned, ReleaseSHA: signed.ReleaseSHA, GenerationArtifactSHA256: strings.Repeat("a", 64), WorkflowRunID: "1", WorkflowRunAttempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestSignedPhaseStateRoundTripAndBundleRecoveryTogether is B08's central
// integration proof: persist a real `signed` Record (with real
// Bundle/TagRef data, not placeholder zero values) through
// GitStateStore against a local bare state-fixture remote, fetch it into
// a brand-new GitStateStore instance simulating a clean clone, confirm
// the round-tripped SignedOutput is byte-identical, and only then
// independently verify the recovery bundle restores the exact release
// SHA — proving the `signed` phase is genuinely ready for a hypothetical
// branch-publication step (B09), without this test performing any
// publication itself.
func TestSignedPhaseStateRoundTripAndBundleRecoveryTogether(t *testing.T) {
	sourceRemotePath, output, signResult, generatingWorkDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(context.Background(), ExecRunner{}, BuildRecoveryBundleInput{
		WorkDir:    generatingWorkDir,
		SourceSHA:  output.SourceSHA,
		ReleaseSHA: signResult.SignedOutput.ReleaseSHA,
		Tags:       signResult.SignedOutput.Tags,
		OutputPath: bundlePath,
	})
	if err != nil {
		t.Fatalf("BuildRecoveryBundle: %v", err)
	}
	signed := signResult.SignedOutput
	signed.Bundle = bundle
	signed.Bundle.Path = "requests/123/789/recovery.bundle"

	stateRemote := newBareFixtureRemote(t)
	seedStateBranch(t, stateRemote, StateBranch, map[string]string{".keep": "state branch root\n"})
	stateSigner, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	store := newFixtureStore(t, stateRemote, stateSigner)
	ctx := context.Background()

	reservation := reservationForSigned("v2.9.0-dev", signed.SourceSHA)
	loaded, err := store.LoadState(ctx, reservation.RequestKey)
	if err != nil {
		t.Fatal(err)
	}
	reserveDecision := sealedReservationDecision(t, reservation, signed.SignerFingerprint)
	reservedHead, err := store.PersistReservation(ctx, reserveDecision, loaded.RemoteHead)
	if err != nil {
		t.Fatalf("persist reservation: %v", err)
	}

	reloadedAfterReserve, err := store.LoadState(ctx, reservation.RequestKey)
	if err != nil {
		t.Fatal(err)
	}
	signedRecord, alreadySigned, err := AdvanceToSigned(reloadedAfterReserve.Record, signed, signedRecordEvidence(t, signed))
	if err != nil {
		t.Fatal(err)
	}
	signed = *signedRecord.SignedOutput
	if alreadySigned {
		t.Fatal("unexpected already-signed result on first transition")
	}
	signedDecision := ReservationDecision{Reserved: true, Record: signedRecord}
	signedHead, err := store.PersistReservation(ctx, signedDecision, reservedHead)
	if err != nil {
		t.Fatalf("persist signed record: %v", err)
	}

	// Simulate "a clean clone": a brand-new GitStateStore instance with
	// its own fresh workDir, never reusing store's.
	cleanCloneStore := newFixtureStore(t, stateRemote, stateSigner)
	reloaded, err := cleanCloneStore.LoadState(ctx, reservation.RequestKey)
	if err != nil {
		t.Fatalf("clean clone LoadState: %v", err)
	}
	if !reloaded.Found || reloaded.RemoteHead != signedHead {
		t.Fatalf("clean clone did not observe signed state: %#v", reloaded)
	}
	if reloaded.Record.Phase != PhaseSigned {
		t.Fatalf("phase = %q, want signed", reloaded.Record.Phase)
	}
	if reloaded.Record.SignedOutput == nil || !reflect.DeepEqual(*reloaded.Record.SignedOutput, signed) {
		t.Fatalf("round-tripped SignedOutput = %#v, want %#v", reloaded.Record.SignedOutput, signed)
	}

	// Only now, after the state round trip is proven, independently
	// verify the bundle recovers the exact release SHA: delete the
	// generating workdir first, matching B08's core recovery proof.
	if err := os.RemoveAll(generatingWorkDir); err != nil {
		t.Fatalf("remove generating workdir: %v", err)
	}
	if _, err := os.Stat(generatingWorkDir); !os.IsNotExist(err) {
		t.Fatalf("generating workdir still exists: %v", err)
	}
	freshWorkDir := t.TempDir()
	if err := RestoreFromBundle(ctx, ExecRunner{}, RestoreFromBundleInput{
		FreshWorkDir:       freshWorkDir,
		SourceRemotePath:   sourceRemotePath,
		SourceSHA:          output.SourceSHA,
		BundlePath:         bundlePath,
		ExpectedBundle:     reloaded.Record.SignedOutput.Bundle,
		ExpectedReleaseSHA: reloaded.Record.SignedOutput.ReleaseSHA,
		ExpectedTreeSHA:    reloaded.Record.SignedOutput.TreeSHA,
		ExpectedTags:       reloaded.Record.SignedOutput.Tags,
	}); err != nil {
		t.Fatalf("RestoreFromBundle after state round trip: %v", err)
	}
}

// TestAdvanceToSignedRejectsResigningAnAlreadySignedOperation proves "Do
// not regenerate timestamps, re-sign, or rebuild a recorded signed commit
// during resume": calling AdvanceToSigned twice against a record already
// at PhaseSigned, with the same SignedOutput, reports alreadySigned=true
// and returns the *existing* record unchanged, without ever needing to
// call SignCommit or Signer.Sign again. A caller that only checks
// alreadySigned's return value has no reason to invoke signing at all on
// the second call, which is what this test proves is safe to do.
func TestAdvanceToSignedRejectsResigningAnAlreadySignedOperation(t *testing.T) {
	_, _, signResult, _ := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	signed := signResult.SignedOutput
	signed.Bundle = Bundle{Path: "requests/123/789/recovery.bundle", SHA256: "abc", SizeBytes: 10, PrerequisiteSHAs: []string{signed.SourceSHA}}

	reservation := reservationForSigned("v2.9.0-dev", signed.SourceSHA)
	record := Record{Reservation: reservation, Phase: PhaseReserved}
	firstRecord, alreadySigned, err := AdvanceToSigned(record, signed, signedRecordEvidence(t, signed))
	if err != nil {
		t.Fatal(err)
	}
	if alreadySigned {
		t.Fatal("unexpected already-signed on first transition")
	}
	if firstRecord.Phase != PhaseSigned || firstRecord.SignedOutput == nil {
		t.Fatalf("unexpected first record: %#v", firstRecord)
	}

	secondRecord, alreadySignedRetry, err := AdvanceToSigned(firstRecord, signed, signedRecordEvidence(t, signed))
	if err != nil {
		t.Fatal(err)
	}
	if !alreadySignedRetry {
		t.Fatal("expected alreadySigned=true on retry against an existing signed record")
	}
	if !reflect.DeepEqual(secondRecord, firstRecord) {
		t.Fatalf("retry returned a different record: %#v, want %#v", secondRecord, firstRecord)
	}
}

// TestAdvanceToSignedRejectsDifferentSignedOutputOnExistingSignedRecord
// proves the "signed output, once recorded, is also immutable for this
// operation" check: a record already at PhaseSigned cannot be advanced
// again with a *different* SignedOutput (e.g. a different release SHA),
// even though ReserveOperation's own immutableReservationDiff never
// looks at SignedOutput at all (it is not one of Reservation's fields).
func TestAdvanceToSignedRejectsDifferentSignedOutputOnExistingSignedRecord(t *testing.T) {
	_, _, signResult, _ := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	signed := signResult.SignedOutput

	reservation := reservationForSigned("v2.9.0-dev", signed.SourceSHA)
	record := Record{Reservation: reservation, Phase: PhaseReserved}
	firstRecord, _, err := AdvanceToSigned(record, signed, signedRecordEvidence(t, signed))
	if err != nil {
		t.Fatal(err)
	}

	different := signed
	different.ReleaseSHA = differentValidSHA(signed.ReleaseSHA)
	if _, _, err := AdvanceToSigned(firstRecord, different, signedRecordEvidence(t, different)); ErrorCode(err) != "signed_output_changed" {
		t.Fatalf("error = %q, want signed_output_changed", ErrorCode(err))
	}
}

// TestAdvanceToSignedRejectsRegressingFromLaterPhase proves AdvanceToSigned
// cannot move a record already past `signed` (e.g. branches_published)
// backwards to `signed`, matching §13.5's phase progression being
// strictly forward-only even when a caller mistakenly retries an old
// signing step against a more-advanced record.
func TestAdvanceToSignedRejectsRegressingFromLaterPhase(t *testing.T) {
	_, _, signResult, _ := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	signed := signResult.SignedOutput
	reservation := reservationForSigned("v2.9.0-dev", signed.SourceSHA)
	record := Record{Reservation: reservation, Phase: PhaseBranchesPublished, SignedOutput: &signed}
	if _, _, err := AdvanceToSigned(record, signed, signedRecordEvidence(t, signed)); ErrorCode(err) != "phase_regression" {
		t.Fatalf("error = %q, want phase_regression", ErrorCode(err))
	}
}

func differentValidSHA(sha string) string {
	if sha == "" {
		return "0000000000000000000000000000000000000000"
	}
	runes := []byte(sha)
	if runes[0] == 'f' {
		runes[0] = '0'
	} else {
		runes[0] = 'f'
	}
	return string(runes)
}

// TestFailureBeforeDurableStatePushDoesNotPersistSignedPhase models
// failure injection immediately before the durable state push: the
// signed commit, tags, and bundle are all built locally (SignCommit and
// BuildRecoveryBundle both succeed), but PersistReservation is never
// called (standing in for a crash before that push). A fresh LoadState
// against the same state remote proves the operation is still only at
// PhaseReserved: an interrupted signing attempt is not visible as
// `signed` until the state push actually succeeds. A subsequent retry
// (calling SignCommit again from the same validated inputs) produces a
// signed commit sharing the identical tree/parent as the first attempt;
// only the object's own SHA can differ if wall-clock author time
// differs between the two attempts, which is expected and explicitly
// tolerated here rather than treated as nondeterminism to hide.
func TestFailureBeforeDurableStatePushDoesNotPersistSignedPhase(t *testing.T) {
	output, reader, manifest, firstResult := signWithFixture(t, "dev-v2.9.x", "v2.9.0-dev", ephemeralTestSigner(t))
	_ = firstResult // stands in for the "locally built but never pushed" signed commit

	stateRemote := newBareFixtureRemote(t)
	seedStateBranch(t, stateRemote, StateBranch, map[string]string{".keep": "state branch root\n"})
	stateSigner, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	store := newFixtureStore(t, stateRemote, stateSigner)
	ctx := context.Background()

	reservation := reservationForSigned("v2.9.0-dev", output.SourceSHA)
	loaded, err := store.LoadState(ctx, reservation.RequestKey)
	if err != nil {
		t.Fatal(err)
	}
	reserveDecision := sealedReservationDecision(t, reservation)
	if _, err := store.PersistReservation(ctx, reserveDecision, loaded.RemoteHead); err != nil {
		t.Fatalf("persist reservation: %v", err)
	}

	// "Crash" here: SignCommit already ran locally (firstResult above),
	// but the state push for the `signed` transition never happens.
	reloaded, err := store.LoadState(ctx, reservation.RequestKey)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Record.Phase != PhaseReserved {
		t.Fatalf("phase after interrupted signing = %q, want reserved (signed phase must not be visible before the state push)", reloaded.Record.Phase)
	}
	if reloaded.Record.SignedOutput != nil {
		t.Fatal("SignedOutput visible before any state push succeeded")
	}

	// Retry from the same validated inputs: SignCommit is deterministic
	// given the same tree/parent/version/committer identity, except for
	// author date (a real timestamp), so only the commit SHA may differ
	// between attempts if wall-clock time advanced; tree/parent are
	// identical either way.
	retryResult, err := SignCommit(ctx, ExecRunner{}, nil, ephemeralTestSigner(t), SignInput{
		Output:          output,
		Manifest:        manifest,
		Reader:          reader.Read,
		WorkDir:         reader.Dir,
		ToolDigest:      strings.Repeat("a", 64),
		ValidatorDigest: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatalf("retry SignCommit: %v", err)
	}
	if retryResult.SignedOutput.TreeSHA != firstResult.SignedOutput.TreeSHA {
		t.Fatalf("retry tree = %s, want identical tree %s", retryResult.SignedOutput.TreeSHA, firstResult.SignedOutput.TreeSHA)
	}
	if retryResult.SignedOutput.CommitParentSHA != firstResult.SignedOutput.CommitParentSHA {
		t.Fatalf("retry parent = %s, want identical parent %s", retryResult.SignedOutput.CommitParentSHA, firstResult.SignedOutput.CommitParentSHA)
	}
	// The discarded first attempt's signed commit is not canonical: only
	// whichever attempt's record actually gets pushed through
	// PersistReservation becomes the operation's recorded SignedOutput.
	// Neither attempt has been persisted in this test, which is exactly
	// the point: an unpushed signed commit, however many times it was
	// (re)built locally, never becomes canonical on its own.
}

// TestFailureAfterDurableStatePushRecoversByteIdenticalSignedOutput
// models failure injection immediately after the durable state push:
// persist the `signed` record successfully, then reload from a fresh
// store (simulating a new runner/clone) and confirm every
// SignedOutput/Bundle/TagRef field is recovered byte-for-byte. This is
// state.go's S06 (B06) now exercised with real Bundle/TagRef data
// instead of placeholder zero values.
func TestFailureAfterDurableStatePushRecoversByteIdenticalSignedOutput(t *testing.T) {
	sourceRemotePath, output, signResult, generatingWorkDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(context.Background(), ExecRunner{}, BuildRecoveryBundleInput{
		WorkDir:    generatingWorkDir,
		SourceSHA:  output.SourceSHA,
		ReleaseSHA: signResult.SignedOutput.ReleaseSHA,
		Tags:       signResult.SignedOutput.Tags,
		OutputPath: bundlePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	signed := signResult.SignedOutput
	signed.Bundle = bundle
	signed.Bundle.Path = "requests/123/789/recovery.bundle"
	_ = sourceRemotePath

	stateRemote := newBareFixtureRemote(t)
	seedStateBranch(t, stateRemote, StateBranch, map[string]string{".keep": "state branch root\n"})
	stateSigner, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	firstRunnerStore := newFixtureStore(t, stateRemote, stateSigner)
	ctx := context.Background()

	reservation := reservationForSigned("v2.9.0-dev", signed.SourceSHA)
	loaded, err := firstRunnerStore.LoadState(ctx, reservation.RequestKey)
	if err != nil {
		t.Fatal(err)
	}
	reserveDecision := sealedReservationDecision(t, reservation, signed.SignerFingerprint)
	reservedHead, err := firstRunnerStore.PersistReservation(ctx, reserveDecision, loaded.RemoteHead)
	if err != nil {
		t.Fatal(err)
	}
	reloadedAfterReserve, err := firstRunnerStore.LoadState(ctx, reservation.RequestKey)
	if err != nil {
		t.Fatal(err)
	}
	signedRecord, _, err := AdvanceToSigned(reloadedAfterReserve.Record, signed, signedRecordEvidence(t, signed))
	if err != nil {
		t.Fatal(err)
	}
	signed = *signedRecord.SignedOutput
	if _, err := firstRunnerStore.PersistReservation(ctx, ReservationDecision{Reserved: true, Record: signedRecord}, reservedHead); err != nil {
		t.Fatalf("persist signed record: %v", err)
	}

	// "The first runner" (its work directory) is gone; a fresh store
	// stands in for a fresh runner picking up the same request key,
	// exactly mirroring state_git_test.go's S06/S07 pattern.
	secondRunnerStore := newFixtureStore(t, stateRemote, stateSigner)
	reloaded, err := secondRunnerStore.LoadState(ctx, reservation.RequestKey)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Found || reloaded.Record.Phase != PhaseSigned {
		t.Fatalf("second runner did not recover signed phase: %#v", reloaded.Record)
	}
	if reloaded.Record.SignedOutput == nil {
		t.Fatal("second runner recovered nil SignedOutput")
	}
	if !reflect.DeepEqual(*reloaded.Record.SignedOutput, signed) {
		t.Fatalf("recovered SignedOutput = %#v, want byte-identical %#v", *reloaded.Record.SignedOutput, signed)
	}
	if len(reloaded.Record.SignedOutput.Tags) == 0 || len(reloaded.Record.SignedOutput.Bundle.PrerequisiteSHAs) == 0 {
		t.Fatal("recovered SignedOutput has placeholder/empty Tags or Bundle, not real data")
	}
}
