// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type b09Fixture struct {
	workDir     string
	sourceSHA   string
	unsignedSHA string
	releaseSHA  string
	treeSHA     string
	tags        []TagRef
}

func newB09Fixture(t *testing.T) b09Fixture {
	t.Helper()
	workDir := t.TempDir()
	runGit(t, workDir, "init", "--quiet", "-b", "fixture")
	blob := strings.TrimSpace(runGitStdin(t, workDir, "source\n", "hash-object", "-w", "--stdin"))
	sourceTree := strings.TrimSpace(runGitStdin(t, workDir, "100644 blob "+blob+"\tREADME.md\n", "mktree"))
	sourceSHA := strings.TrimSpace(runGitWithIdentity(t, workDir, "commit-tree", sourceTree, "-m", "source"))
	newBlob := strings.TrimSpace(runGitStdin(t, workDir, "release\n", "hash-object", "-w", "--stdin"))
	releaseTree := strings.TrimSpace(runGitStdin(t, workDir, "100644 blob "+newBlob+"\tREADME.md\n", "mktree"))
	unsignedSHA := strings.TrimSpace(runGitWithIdentity(t, workDir, "commit-tree", releaseTree, "-p", sourceSHA, "-m", "unsigned release"))
	releaseSHA := strings.TrimSpace(runGitWithIdentity(t, workDir, "commit-tree", releaseTree, "-p", sourceSHA, "-m", "signed release"))

	names := []string{"v2.9.0", "moduleA/v2.9.0", "moduleB/v2.9.0"}
	tags := make([]TagRef, 0, len(names))
	for _, name := range names {
		runGitWithIdentity(t, workDir, "-c", "tag.gpgsign=false", "tag", "-a", "-m", name, name, releaseSHA)
		tagObject := strings.TrimSpace(runGit(t, workDir, "rev-parse", "refs/tags/"+name))
		peeled := strings.TrimSpace(runGit(t, workDir, "rev-parse", "refs/tags/"+name+"^{commit}"))
		tags = append(tags, TagRef{Name: name, Ref: "refs/tags/" + name, TagObjectSHA: tagObject, PeeledCommitSHA: peeled})
	}
	return b09Fixture{workDir: workDir, sourceSHA: sourceSHA, unsignedSHA: unsignedSHA, releaseSHA: releaseSHA, treeSHA: releaseTree, tags: tags}
}

func (f b09Fixture) record(command string, phase OperationPhase) Record {
	reservation := baseReservation()
	reservation.Command = command
	reservation.ReleaseLine = "v2.9"
	reservation.ResolvedVersion = "v2.9.0"
	signed := SignedOutput{
		UnsignedSHA:       f.unsignedSHA,
		SourceSHA:         f.sourceSHA,
		TreeSHA:           f.treeSHA,
		ReleaseSHA:        f.releaseSHA,
		CommitParentSHA:   f.sourceSHA,
		SignerFingerprint: strings.Repeat("a", 64),
		Tags:              append([]TagRef(nil), f.tags...),
	}
	switch command {
	case "release:prepare":
		reservation.BranchIntents = []BranchIntent{
			{Ref: "refs/heads/release-v2.9.x", DesiredSHA: f.sourceSHA},
			{Ref: "refs/heads/dev-v2.10.x", DesiredSHA: f.releaseSHA},
		}
	case "release:promote", "release:release":
		reservation.BranchIntents = []BranchIntent{{Ref: "refs/heads/release-v2.9.x", ExpectedOldSHA: f.sourceSHA, DesiredSHA: f.releaseSHA}}
	}
	return Record{Reservation: reservation, Phase: phase, SignedOutput: &signed}
}

func (f b09Fixture) verified(command string, phase OperationPhase) VerifiedPublication {
	return VerifiedPublication{record: f.record(command, phase), workDir: f.workDir}
}

func testEvidence(releaseSHA string) VerifiedTestEvidence {
	return VerifiedTestEvidence{SchemaVersion: "1", ReleaseSHA: releaseSHA, EvidenceSHA256: strings.Repeat("b", 64)}
}

func pushFixtureRef(t *testing.T, workDir, remote, source, ref string) {
	t.Helper()
	runGit(t, workDir, "-c", "push.followTags=false", "push", "--no-follow-tags", remote, source+":"+ref)
}

func remoteRefSHA(t *testing.T, workDir, remote, ref string) string {
	t.Helper()
	sha, found, err := readRemoteRef(context.Background(), ExecRunner{}, workDir, remote, ref)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		return ""
	}
	return sha
}

func TestVerifyPublicationRequiresRemoteSignedRecordAndVerifiedBundle(t *testing.T) {
	ctx := context.Background()
	fixture := newB09Fixture(t)
	signer := ephemeralTestSigner(t)
	signed := fixture.record("release:promote", PhaseSigned).SignedOutput
	fingerprint, _, err := signAttestation(signer, signedAttestation{
		UnsignedSHA: signed.UnsignedSHA, SourceSHA: signed.SourceSHA, TreeSHA: signed.TreeSHA,
		ReleaseSHA: signed.ReleaseSHA, ResolvedVersion: "v2.9.0", Tags: tagRefNames(signed.Tags),
	})
	if err != nil {
		t.Fatal(err)
	}
	signed.SignerFingerprint = fingerprint
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(ctx, ExecRunner{}, BuildRecoveryBundleInput{
		WorkDir: fixture.workDir, SourceSHA: fixture.sourceSHA, ReleaseSHA: fixture.releaseSHA,
		Tags: fixture.tags, OutputPath: bundlePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	signed.Bundle = bundle
	record := fixture.record("release:promote", PhaseSigned)
	record.SignedOutput = signed
	decision, err := ReserveOperation(nil, record.Reservation, nil)
	if err != nil {
		t.Fatal(err)
	}
	decision.Record.Phase = PhaseSigned
	decision.Record.SignedOutput = signed
	events, err := AppendEvent(decision.Record.Events, record.Reservation.RequestKey, EventPhaseAdvanced, nil)
	if err != nil {
		t.Fatal(err)
	}
	decision.Record.Events = events

	stateRemote := newBareFixtureRemote(t)
	seedStateBranch(t, stateRemote, StateBranch, map[string]string{".keep": "state\n"})
	stateSigner := ephemeralTestSigner(t)
	store := newFixtureStore(t, stateRemote, stateSigner)
	loaded, err := store.LoadState(ctx, record.Reservation.RequestKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PersistReservation(ctx, decision, loaded.RemoteHead); err != nil {
		t.Fatal(err)
	}
	reloaded, err := store.LoadState(ctx, record.Reservation.RequestKey)
	if err != nil {
		t.Fatal(err)
	}
	attestation, err := Seal(signer, mustMarshalAttestationForTest(t, *signed))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyPublication(ctx, ExecRunner{}, PublicationVerificationInput{
		Loaded: reloaded, Attestation: attestation, TrustedPublicKey: signer.PublicKey(), Verifier: Ed25519Verifier{},
		BundlePath: bundlePath, WorkDir: fixture.workDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if verified.record.SignedOutput.ReleaseSHA != fixture.releaseSHA {
		t.Fatalf("verified release = %s", verified.record.SignedOutput.ReleaseSHA)
	}

	invalidUnsigned := reloaded
	invalidSignedOutput := *reloaded.Record.SignedOutput
	invalidSignedOutput.UnsignedSHA = invalidSignedOutput.ReleaseSHA
	invalidUnsigned.Record.SignedOutput = &invalidSignedOutput
	if _, err := VerifyPublication(ctx, ExecRunner{}, PublicationVerificationInput{Loaded: invalidUnsigned}); ErrorCode(err) != "invalid_signed_output" {
		t.Fatalf("error = %q, want invalid_signed_output when unsigned and release SHAs match", ErrorCode(err))
	}

	reloaded.RemoteHead = ""
	if _, err := VerifyPublication(ctx, ExecRunner{}, PublicationVerificationInput{Loaded: reloaded}); ErrorCode(err) != "signed_record_not_remote" {
		t.Fatalf("error = %q, want signed_record_not_remote", ErrorCode(err))
	}
}

// P01: unexpected branch movement conflicts and is never overwritten.
func TestPublishBranchesP01ChangedBranchConflictsWithoutForce(t *testing.T) {
	fixture := newB09Fixture(t)
	remote := newBareFixtureRemote(t)
	humanTree := strings.TrimSpace(runGit(t, fixture.workDir, "rev-parse", fixture.sourceSHA+"^{tree}"))
	humanCommit := strings.TrimSpace(runGitWithIdentity(t, fixture.workDir, "commit-tree", humanTree, "-p", fixture.sourceSHA, "-m", "human"))
	pushFixtureRef(t, fixture.workDir, remote, humanCommit, "refs/heads/release-v2.9.x")

	_, err := PublishBranches(context.Background(), ExecRunner{}, fixture.verified("release:promote", PhaseSigned), remote)
	if ErrorCode(err) != "branch_ref_conflict" || ClassOf(err) != ErrorClassStateConflict {
		t.Fatalf("error = %q/%q", ClassOf(err), ErrorCode(err))
	}
	if got := remoteRefSHA(t, fixture.workDir, remote, "refs/heads/release-v2.9.x"); got != humanCommit {
		t.Fatalf("conflicting branch changed: got %s want %s", got, humanCommit)
	}
}

// P02: a same-name tag with the wrong object/peeled commit is a conflict.
func TestPublishTagsP02ConflictingTagRemainsUnchanged(t *testing.T) {
	fixture := newB09Fixture(t)
	remote := newBareFixtureRemote(t)
	first := fixture.tags[0]
	pushFixtureRef(t, fixture.workDir, remote, fixture.sourceSHA, first.Ref)
	before := remoteRefSHA(t, fixture.workDir, remote, first.Ref)

	_, err := PublishTags(context.Background(), ExecRunner{}, fixture.verified("release:promote", PhaseTestsPassed), remote, testEvidence(fixture.releaseSHA))
	if ErrorCode(err) != "tag_ref_conflict" || ClassOf(err) != ErrorClassStateConflict {
		t.Fatalf("error = %q/%q", ClassOf(err), ErrorCode(err))
	}
	if after := remoteRefSHA(t, fixture.workDir, remote, first.Ref); after != before {
		t.Fatalf("conflicting tag changed: before %s after %s", before, after)
	}
}

type recordingPushRunner struct {
	runner   CommandRunner
	commands []Command
}

func (r *recordingPushRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if isPushCommand(command) {
		r.commands = append(r.commands, command)
	}
	return r.runner.Run(ctx, command)
}

func TestPublishBranchesUsesOnlyApprovedPushShapes(t *testing.T) {
	t.Run("prepare create-only empty leases", func(t *testing.T) {
		fixture := newB09Fixture(t)
		remote := newBareFixtureRemote(t)
		runner := &recordingPushRunner{runner: ExecRunner{}}
		result, err := PublishBranches(context.Background(), runner, fixture.verified("release:prepare", PhaseSigned), remote)
		if err != nil {
			t.Fatal(err)
		}
		if result.Record.Phase != PhaseBranchesPublished || len(runner.commands) != 2 {
			t.Fatalf("result=%#v commands=%d", result, len(runner.commands))
		}
		for _, command := range runner.commands {
			foundLease := false
			for _, arg := range command.Args {
				if strings.HasPrefix(arg, "--force-with-lease=refs/heads/") && strings.HasSuffix(arg, ":") {
					foundLease = true
				}
			}
			if !foundLease || ValidatePushCommand(command) != nil {
				t.Fatalf("unsafe create command: %#v", command.Args)
			}
		}
	})
	t.Run("promote normal fast-forward without lease", func(t *testing.T) {
		fixture := newB09Fixture(t)
		remote := newBareFixtureRemote(t)
		pushFixtureRef(t, fixture.workDir, remote, fixture.sourceSHA, "refs/heads/release-v2.9.x")
		runner := &recordingPushRunner{runner: ExecRunner{}}
		result, err := PublishBranches(context.Background(), runner, fixture.verified("release:promote", PhaseSigned), remote)
		if err != nil {
			t.Fatal(err)
		}
		if result.Record.Phase != PhaseBranchesPublished || len(runner.commands) != 1 || remoteRefSHA(t, fixture.workDir, remote, "refs/heads/release-v2.9.x") != fixture.releaseSHA {
			t.Fatalf("result=%#v commands=%d", result, len(runner.commands))
		}
		for _, arg := range runner.commands[0].Args {
			if strings.HasPrefix(arg, "--force") {
				t.Fatalf("update command contains lease/force: %#v", runner.commands[0].Args)
			}
		}
	})
}

type dropPushResponseRunner struct {
	runner CommandRunner
	drop   bool
}

func (r *dropPushResponseRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	result, err := r.runner.Run(ctx, command)
	if err == nil && r.drop && isPushCommand(command) {
		r.drop = false
		return result, errors.New("simulated lost push response")
	}
	return result, err
}

// P03: the push is real, but its successful response is replaced by an error;
// the publisher re-reads the remote and recognizes the desired ref.
func TestPublishBranchesP03AcceptedPushWithLostResponseReconciles(t *testing.T) {
	fixture := newB09Fixture(t)
	remote := newBareFixtureRemote(t)
	runner := &dropPushResponseRunner{runner: ExecRunner{}, drop: true}
	result, err := PublishBranches(context.Background(), runner, fixture.verified("release:prepare", PhaseSigned), remote)
	if err != nil {
		t.Fatal(err)
	}
	if result.Record.Phase != PhaseBranchesPublished || remoteRefSHA(t, fixture.workDir, remote, "refs/heads/release-v2.9.x") != fixture.sourceSHA {
		t.Fatalf("lost-response push was not reconciled: %#v", result)
	}
}

type interruptPushRunner struct {
	runner CommandRunner
	cancel context.CancelFunc
	target int
	after  bool
	seen   int
}

func (r *interruptPushRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if !isPushCommand(command) {
		return r.runner.Run(ctx, command)
	}
	index := r.seen
	r.seen++
	if index != r.target {
		return r.runner.Run(ctx, command)
	}
	if !r.after {
		r.cancel()
		return CommandResult{}, context.Canceled
	}
	result, err := r.runner.Run(context.Background(), command)
	r.cancel()
	if err != nil {
		return result, err
	}
	return result, context.Canceled
}

type countPushRunner struct {
	runner CommandRunner
	pushes int
}

func (r *countPushRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if isPushCommand(command) {
		r.pushes++
	}
	return r.runner.Run(ctx, command)
}

func isPushCommand(command Command) bool {
	for _, arg := range command.Args {
		if arg == "push" {
			return true
		}
	}
	return false
}

// P04: interrupt before and after every individual tag push, then resume and
// push only refs still absent from the recorded manifest.
func TestPublishTagsP04ResumeOnlyMissingTagsAfterEveryFailureIndex(t *testing.T) {
	for _, after := range []bool{false, true} {
		for target := 0; target < 3; target++ {
			name := "before"
			if after {
				name = "after"
			}
			t.Run(name+"_tag_"+string(rune('0'+target)), func(t *testing.T) {
				fixture := newB09Fixture(t)
				remote := newBareFixtureRemote(t)
				ctx, cancel := context.WithCancel(context.Background())
				runner := &interruptPushRunner{runner: ExecRunner{}, cancel: cancel, target: target, after: after}
				_, err := PublishTags(ctx, runner, fixture.verified("release:promote", PhaseTestsPassed), remote, testEvidence(fixture.releaseSHA))
				if err == nil {
					t.Fatal("interrupted publication succeeded")
				}
				counter := &countPushRunner{runner: ExecRunner{}}
				result, err := PublishTags(context.Background(), counter, fixture.verified("release:promote", PhaseTestsPassed), remote, testEvidence(fixture.releaseSHA))
				if err != nil {
					t.Fatal(err)
				}
				alreadyPublished := target
				if after {
					alreadyPublished++
				}
				if counter.pushes != len(fixture.tags)-alreadyPublished {
					t.Fatalf("resume pushes = %d, want %d", counter.pushes, len(fixture.tags)-alreadyPublished)
				}
				if result.Record.Phase != PhaseTagsPublished {
					t.Fatalf("phase = %q", result.Record.Phase)
				}
			})
		}
	}
}

// P05: delete the generating checkout, restore exact objects from B08's
// bundle into a new repository, then publish from only that recovered clone.
func TestPublishTagsP05ResumeAfterOriginalCloneRemoved(t *testing.T) {
	sourceRemote, output, signResult, generatingDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(context.Background(), ExecRunner{}, BuildRecoveryBundleInput{
		WorkDir: generatingDir, SourceSHA: output.SourceSHA, ReleaseSHA: signResult.SignedOutput.ReleaseSHA,
		Tags: signResult.SignedOutput.Tags, OutputPath: bundlePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(generatingDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generatingDir); !os.IsNotExist(err) {
		t.Fatalf("generating clone still exists: %v", err)
	}
	recovered := t.TempDir()
	if err := RestoreFromBundle(context.Background(), ExecRunner{}, RestoreFromBundleInput{
		FreshWorkDir: recovered, SourceRemotePath: sourceRemote, SourceSHA: output.SourceSHA,
		BundlePath: bundlePath, ExpectedBundle: bundle, ExpectedReleaseSHA: signResult.SignedOutput.ReleaseSHA,
		ExpectedTreeSHA: signResult.SignedOutput.TreeSHA, ExpectedTags: signResult.SignedOutput.Tags,
	}); err != nil {
		t.Fatal(err)
	}
	record := Record{Reservation: reservationForSigned("v2.9.0-dev"), Phase: PhaseTestsPassed, SignedOutput: &signResult.SignedOutput}
	record.SignedOutput.Bundle = bundle
	remote := newBareFixtureRemote(t)
	result, err := PublishTags(context.Background(), ExecRunner{}, VerifiedPublication{record: record, workDir: recovered}, remote, testEvidence(signResult.SignedOutput.ReleaseSHA))
	if err != nil {
		t.Fatal(err)
	}
	if result.Record.Phase != PhaseTagsPublished || len(result.PublishedRefs) != len(signResult.SignedOutput.Tags) {
		t.Fatalf("recovery publication = %#v", result)
	}
}

type createBranchRaceRunner struct {
	runner    CommandRunner
	workDir   string
	remote    string
	sourceSHA string
	targetRef string
	once      sync.Once
}

func (r *createBranchRaceRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	if isPushCommand(command) && containsArg(command.Args, "--force-with-lease="+r.targetRef+":") {
		r.once.Do(func() {
			_, _ = ExecRunner{}.Run(context.Background(), Command{
				Path: "git", Args: []string{"-c", "protocol.file.allow=always", "-c", "push.followTags=false", "push", "--no-follow-tags", r.remote, r.sourceSHA + ":" + r.targetRef},
				Env: gitStateEnv(), Dir: r.workDir,
			})
		})
	}
	return r.runner.Run(ctx, command)
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

// P06: another actor creates the dev branch after inspection. The explicit
// empty-value lease rejects the create-only push and leaves their SHA intact.
func TestPublishBranchesP06EmptyLeaseRejectsInterveningCreate(t *testing.T) {
	fixture := newB09Fixture(t)
	remote := newBareFixtureRemote(t)
	target := "refs/heads/dev-v2.10.x"
	runner := &createBranchRaceRunner{runner: ExecRunner{}, workDir: fixture.workDir, remote: remote, sourceSHA: fixture.sourceSHA, targetRef: target}
	_, err := PublishBranches(context.Background(), runner, fixture.verified("release:prepare", PhaseSigned), remote)
	if ErrorCode(err) != "branch_ref_conflict" || ClassOf(err) != ErrorClassStateConflict {
		t.Fatalf("error = %q/%q", ClassOf(err), ErrorCode(err))
	}
	if got := remoteRefSHA(t, fixture.workDir, remote, target); got != fixture.sourceSHA {
		t.Fatalf("intervening branch changed: got %s want %s", got, fixture.sourceSHA)
	}
}

// The prepare branch matrix interrupts before and after each of its two
// ordered pushes, then proves resume creates only still-missing refs.
func TestPublishBranchesFailuresBeforeAndAfterEveryPreparePush(t *testing.T) {
	for _, after := range []bool{false, true} {
		for target := 0; target < 2; target++ {
			fixture := newB09Fixture(t)
			remote := newBareFixtureRemote(t)
			ctx, cancel := context.WithCancel(context.Background())
			runner := &interruptPushRunner{runner: ExecRunner{}, cancel: cancel, target: target, after: after}
			_, err := PublishBranches(ctx, runner, fixture.verified("release:prepare", PhaseSigned), remote)
			if err == nil {
				t.Fatal("interrupted publication succeeded")
			}
			counter := &countPushRunner{runner: ExecRunner{}}
			result, err := PublishBranches(context.Background(), counter, fixture.verified("release:prepare", PhaseSigned), remote)
			if err != nil {
				t.Fatal(err)
			}
			alreadyPublished := target
			if after {
				alreadyPublished++
			}
			if counter.pushes != 2-alreadyPublished || result.Record.Phase != PhaseBranchesPublished {
				t.Fatalf("resume pushes=%d phase=%q", counter.pushes, result.Record.Phase)
			}
		}
	}
}

func TestPublishTagsRequiresStructurallyVerifiedEvidence(t *testing.T) {
	fixture := newB09Fixture(t)
	remote := newBareFixtureRemote(t)
	_, err := PublishTags(context.Background(), ExecRunner{}, fixture.verified("release:promote", PhaseTestsPassed), remote, VerifiedTestEvidence{})
	if ErrorCode(err) != "verified_test_evidence_required" || ClassOf(err) != ErrorClassTestGateFailed {
		t.Fatalf("error = %q/%q", ClassOf(err), ErrorCode(err))
	}
	if got := remoteRefSHA(t, fixture.workDir, remote, fixture.tags[0].Ref); got != "" {
		t.Fatalf("tag published without evidence: %s", got)
	}
}

func TestDecodeVerifiedTestEvidenceJSONIsStrict(t *testing.T) {
	digest := strings.Repeat("a", 64)
	release := strings.Repeat("b", 40)
	valid := []byte(`{"schema_version":"1","release_sha":"` + release + `","evidence_sha256":"` + digest + `"}`)
	if _, err := DecodeVerifiedTestEvidenceJSON(valid); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{
		[]byte(`{"schema_version":"1","schema_version":"1","release_sha":"` + release + `","evidence_sha256":"` + digest + `"}`),
		[]byte(`{"schema_version":"1","release_sha":null,"evidence_sha256":"` + digest + `"}`),
		[]byte(`{"schema_version":"1","release_sha":"` + release + `","evidence_sha256":"` + digest + `","extra":"x"}`),
	} {
		if _, err := DecodeVerifiedTestEvidenceJSON(raw); ErrorCode(err) != "invalid_test_evidence" {
			t.Fatalf("error = %q, want invalid_test_evidence for %s", ErrorCode(err), raw)
		}
	}
}

func TestValidatePushCommandRejectsUnsafeShapes(t *testing.T) {
	sha := strings.Repeat("a", 40)
	validCreate := Command{Path: "git", Args: []string{"push", "--force-with-lease=refs/heads/release-v2.9.x:", "/tmp/remote.git", sha + ":refs/heads/release-v2.9.x"}}
	if err := ValidatePushCommand(validCreate); err != nil {
		t.Fatal(err)
	}
	unsafe := [][]string{
		{"push", "--force", "/tmp/r", sha + ":refs/heads/release-v2.9.x"},
		{"push", "--force-with-lease", "/tmp/r", sha + ":refs/heads/release-v2.9.x"},
		{"push", "--force-with-lease=refs/heads/release-v2.9.x:" + sha, "/tmp/r", sha + ":refs/heads/release-v2.9.x"},
		{"push", "--force-with-lease=refs/tags/v2.9.0:", "/tmp/r", sha + ":refs/tags/v2.9.0"},
		{"push", "--tags", "/tmp/r"},
		{"push", "/tmp/r", ":refs/heads/release-v2.9.x"},
	}
	for _, args := range unsafe {
		if err := ValidatePushCommand(Command{Path: "git", Args: args}); ErrorCode(err) != "unsafe_push_command" {
			t.Fatalf("args=%v error=%q", args, ErrorCode(err))
		}
	}
}

func TestLaterPublicationPhasesAreVerifyOnly(t *testing.T) {
	t.Run("branches published missing branch", func(t *testing.T) {
		fixture := newB09Fixture(t)
		remote := newBareFixtureRemote(t)
		counter := &countPushRunner{runner: ExecRunner{}}
		_, err := PublishBranches(context.Background(), counter, fixture.verified("release:prepare", PhaseBranchesPublished), remote)
		if ErrorCode(err) != "recorded_branch_ref_mismatch" || counter.pushes != 0 {
			t.Fatalf("error=%q pushes=%d", ErrorCode(err), counter.pushes)
		}
	})
	t.Run("tags published missing tag", func(t *testing.T) {
		fixture := newB09Fixture(t)
		remote := newBareFixtureRemote(t)
		counter := &countPushRunner{runner: ExecRunner{}}
		_, err := PublishTags(context.Background(), counter, fixture.verified("release:promote", PhaseTagsPublished), remote, testEvidence(fixture.releaseSHA))
		if ErrorCode(err) != "recorded_tag_ref_mismatch" || counter.pushes != 0 {
			t.Fatalf("error=%q pushes=%d", ErrorCode(err), counter.pushes)
		}
	})
}

func TestPublishedEventsAreAppendOnlyAndDeduplicatedOnReconcile(t *testing.T) {
	fixture := newB09Fixture(t)
	remote := newBareFixtureRemote(t)
	first, err := PublishBranches(context.Background(), ExecRunner{}, fixture.verified("release:prepare", PhaseSigned), remote)
	if err != nil {
		t.Fatal(err)
	}
	if first.Record.Phase != PhaseBranchesPublished {
		t.Fatalf("phase = %q", first.Record.Phase)
	}
	count := 0
	for _, event := range first.Record.Events {
		if event.Kind == EventBranchPublished {
			count++
		}
	}
	if count != 2 || VerifyEventChain(first.Record.Reservation.RequestKey, first.Record.Events) != nil {
		t.Fatalf("events = %#v", first.Record.Events)
	}
	secondAuth := VerifiedPublication{record: first.Record, workDir: fixture.workDir}
	second, err := PublishBranches(context.Background(), ExecRunner{}, secondAuth, remote)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Record.Events) != len(first.Record.Events) || len(second.PublishedRefs) != 0 {
		t.Fatalf("completed resume changed state or refs: %#v", second)
	}
}
