// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// newBareFixtureRemote creates a local bare Git repository under a fresh
// t.TempDir() to act as the state branch's "remote." It is never a real
// GitHub remote and is torn down automatically with the test's temp dir.
func newBareFixtureRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	if err := exec.Command("git", "init", "--quiet", "--bare", dir).Run(); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	return dir
}

// seedStateBranch creates the state branch on remotePath directly with
// plain `git` commands, deliberately bypassing GitStateStore, so tests can
// set up "a state branch already exists with this content" as a fixture
// precondition without relying on the code under test.
func seedStateBranch(t *testing.T, remotePath, branch string, files map[string]string) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "seed-work")
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = work
		cmd.Env = append(gitStateEnv(), "HOME="+work)
		cmd.Stdin = nil
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	init := exec.Command("git", "init", "--quiet", "-b", branch, work)
	init.Env = append(gitStateEnv(), "HOME="+work)
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("init seed work: %v\n%s", err, out)
	}
	for name, content := range files {
		full := filepath.Join(work, name)
		if err := writeFileWithDirs(full, content); err != nil {
			t.Fatalf("write seed file %s: %v", name, err)
		}
		run("add", name)
	}
	run("-c", "user.name=seed", "-c", "user.email=seed@example.com", "commit", "--quiet", "-m", "seed")
	run("-c", "protocol.file.allow=always", "push", "--quiet", remotePath, branch)
	head := strings.TrimSpace(run("rev-parse", "HEAD"))
	return head
}

func writeFileWithDirs(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func newFixtureStore(t *testing.T, remotePath string, signer Signer) *GitStateStore {
	t.Helper()
	workDir := t.TempDir()
	store := NewGitStateStore(ExecRunner{}, remotePath, StateBranch, workDir, signer, Ed25519Verifier{}, publicKeyOf(t, signer), CommitIdentity{Name: "gardener-release", Email: "gardener-release@example.com"}, nil)
	if err := store.InitWorkingRepository(context.Background()); err != nil {
		t.Fatalf("init working repository: %v", err)
	}
	return store
}

func advanceFixtureRecordToPhase(t *testing.T, decision ReservationDecision, signed SignedOutput, phase OperationPhase) Record {
	t.Helper()
	decision.Record = bindFixtureSigningIntent(t, decision.Record, signed.SignerFingerprint)
	record, _, err := AdvanceToSigned(decision.Record, signed, signedRecordEvidence(t, signed))
	if err != nil {
		t.Fatal(err)
	}
	if phase == PhaseSigned {
		return record
	}
	for _, intent := range record.SignedOutput.PublicationIntents {
		if err := appendRefEvent(&record, EventBranchPublished, intent.Ref, intent.DesiredSHA, "published"); err != nil {
			t.Fatal(err)
		}
	}
	if err := advancePublicationPhase(&record, PhaseBranchesPublished); err != nil {
		t.Fatal(err)
	}
	if phase == PhaseBranchesPublished {
		return record
	}
	targets, err := requiredTestTargets(record)
	if err != nil {
		t.Fatal(err)
	}
	detail := testGateEvidence{SchemaVersion: "1", Repository: RepositoryFullName, WorkflowPath: MainBranchTestWorkflowPath, WorkflowID: "20", WorkflowSHA: strings.Repeat("e", 64), Event: "push", ReleaseSHA: record.SignedOutput.ReleaseSHA, RequiredJobs: []string{"required"}}
	for index, target := range targets {
		detail.Runs = append(detail.Runs, verifiedRunEvidence{TargetBranch: target.Branch, TargetSHA: target.SHA, RunID: fmt.Sprintf("%d", index+1), Attempt: 1, Status: "completed", Conclusion: "success", Jobs: []verifiedJobEvidence{{ID: fmt.Sprintf("%d", index+101), Name: "required", Attempt: 1, Status: "completed", Conclusion: "success"}}})
	}
	canonical, err := canonicalJSON(detail)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	verified := VerifiedTestEvidence{SchemaVersion: "1", ReleaseSHA: record.SignedOutput.ReleaseSHA, EvidenceSHA256: hex.EncodeToString(digest[:])}
	body, err := json.Marshal(testEventEvidence{Evidence: verified, Detail: detail})
	if err != nil {
		t.Fatal(err)
	}
	record.Events, err = AppendEvent(record.Events, record.Reservation.RequestKey, EventTestsPassed, body)
	if err != nil {
		t.Fatal(err)
	}
	if err := advancePublicationPhase(&record, PhaseTestsPassed); err != nil {
		t.Fatal(err)
	}
	if phase == PhaseTestsPassed {
		return record
	}
	for _, tag := range record.SignedOutput.Tags {
		if err := appendRefEvent(&record, EventTagPublished, tag.Ref, tag.TagObjectSHA, "published"); err != nil {
			t.Fatal(err)
		}
	}
	if err := advancePublicationPhase(&record, PhaseTagsPublished); err != nil {
		t.Fatal(err)
	}
	if phase == PhaseTagsPublished {
		return record
	}
	outcome := OperationOutcome{SchemaVersion: "1", Publication: PublicationOutcome{Status: WorkSucceeded, ReleaseSHA: record.SignedOutput.ReleaseSHA}, PreparePR: PreparePROutcome{Status: WorkNotApplicable}, Images: WorkNotApplicable, ImageEvidence: []ImagePromotionEvidence{}}
	if record.Reservation.Command == "release:prepare" {
		dev := record.SignedOutput.PublicationIntents[1]
		pr := preparePREventEvidence{SchemaVersion: "1", Repository: RepositoryFullName, Command: "release:prepare", Number: "42", URL: "https://github.com/DataDog/dd-trace-go/pull/42", Head: strings.TrimPrefix(dev.Ref, "refs/heads/"), SHA: dev.DesiredSHA, Base: "main", Marker: preparePRMarker(record.Reservation.RequestKey), Disposition: "reconciled"}
		body, marshalErr := json.Marshal(pr)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		record.Events, err = AppendEvent(record.Events, record.Reservation.RequestKey, EventPreparePRRecorded, body)
		if err != nil {
			t.Fatal(err)
		}
		outcome.PreparePR = PreparePROutcome{Status: WorkSucceeded, Number: "42"}
	}
	record, err = RecordOperationOutcome(record, outcome)
	if err != nil {
		t.Fatal(err)
	}
	if phase != PhaseComplete || record.Phase != PhaseComplete {
		t.Fatalf("unsupported fixture phase %q", phase)
	}
	return record
}

func publicKeyOf(t *testing.T, signer Signer) []byte {
	t.Helper()
	if signer == nil {
		return nil
	}
	if s, ok := signer.(*Ed25519Signer); ok {
		return s.PublicKey()
	}
	_, key, err := signer.Sign([]byte("probe"))
	if err != nil {
		t.Fatalf("probe signer for public key: %v", err)
	}
	return key
}

func sealedReservationDecision(t *testing.T, reservation Reservation, signerFingerprint ...string) ReservationDecision {
	t.Helper()
	reservation.RequestSHA256 = RequestSHA256(Context{RepositoryID: reservation.RepositoryID, RepositoryFullName: reservation.RepositoryFullName, IssueNumber: reservation.IssueNumber, OriginalCommentID: reservation.OriginalCommentID, AcknowledgementCommentID: reservation.AcknowledgementCommentID, BodySnapshot: reservation.BodySnapshot, PolicyRevision: reservation.PolicyRevision}, reservation.Command, reservation.RequestedVersion)
	decision, err := ReserveOperation(nil, reservation, nil)
	if err != nil {
		t.Fatalf("ReserveOperation: %v", err)
	}
	decision.Record.WorkflowSHA = strings.Repeat("c", 40)
	decision.Record.ToolSHA = strings.Repeat("d", 40)
	fingerprint := strings.Repeat("0", 64)
	if len(signerFingerprint) > 1 {
		t.Fatal("multiple fixture signer fingerprints")
	}
	if len(signerFingerprint) == 1 {
		fingerprint = signerFingerprint[0]
	}
	decision.Record, err = RecordGitSigningIntent(decision.Record, GitSigningIntent{Timestamp: 1767225600, Message: "release: " + reservation.GenerationVersion, Principal: "gardener-release@example.com", Fingerprint: fixtureOpenSSHFingerprint(t, fingerprint)})
	if err != nil {
		t.Fatalf("RecordGitSigningIntent: %v", err)
	}
	return decision
}

func fixtureOpenSSHFingerprint(t *testing.T, fingerprint string) string {
	t.Helper()
	digest, err := hex.DecodeString(fingerprint)
	if err != nil || len(digest) != sha256.Size {
		t.Fatalf("invalid fixture fingerprint %q", fingerprint)
	}
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(digest)
}

func bindFixtureSigningIntent(t *testing.T, record Record, signerFingerprint string) Record {
	t.Helper()
	found := false
	for index := range record.Events {
		if record.Events[index].Kind != EventSigningIntent {
			continue
		}
		if found {
			t.Fatal("duplicate fixture signing intent")
		}
		var intent GitSigningIntent
		if err := json.Unmarshal(record.Events[index].Evidence, &intent); err != nil {
			t.Fatal(err)
		}
		intent.Fingerprint = fixtureOpenSSHFingerprint(t, signerFingerprint)
		body, err := json.Marshal(intent)
		if err != nil {
			t.Fatal(err)
		}
		record.Events[index].Evidence = body
		found = true
	}
	if !found {
		t.Fatal("missing fixture signing intent")
	}
	previous := ""
	for index := range record.Events {
		record.Events[index].Sequence = index + 1
		record.Events[index].PreviousDigest = previous
		record.Events[index].Digest = ""
		digest, err := computeEventDigest(record.Events[index])
		if err != nil {
			t.Fatal(err)
		}
		record.Events[index].Digest = digest
		previous = digest
	}
	return record
}

// TestGitStateStoreS01MissingStateBranchFailsClosed covers §15 S01: a
// state branch that does not exist on the remote is a provisioning error,
// never a trigger to initialize one. LoadState must return an error, not
// Found=false, so a caller cannot confuse "no such request" with "state
// is unavailable."
func TestGitStateStoreS01MissingStateBranchFailsClosed(t *testing.T) {
	remote := newBareFixtureRemote(t)
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	store := newFixtureStore(t, remote, signer)

	_, err = store.LoadState(context.Background(), "123:789")
	if ErrorCode(err) != "state_branch_missing" || ClassOf(err) != ErrorClassStateConflict {
		t.Fatalf("error = %q/%q, want state_conflict state_branch_missing", ClassOf(err), ErrorCode(err))
	}

	decision := sealedReservationDecision(t, baseReservation())
	if _, err := store.PersistReservation(context.Background(), decision, ""); err == nil {
		t.Fatal("PersistReservation succeeded against a missing state branch")
	}
}

func TestGitStateStoreNoRecordRequiresCompleteUnambiguousReadW06(t *testing.T) {
	t.Run("orphan request paths conflict", func(t *testing.T) {
		remote := newBareFixtureRemote(t)
		signer, err := NewEphemeralSigner()
		if err != nil {
			t.Fatal(err)
		}
		seedStateBranch(t, remote, StateBranch, map[string]string{"requests/123/789/events/000001.json": "orphan\n"})
		store := newFixtureStore(t, remote, signer)
		if _, err := store.LoadState(context.Background(), "123:789"); ErrorCode(err) != "ambiguous_request_state" {
			t.Fatalf("error = %q, want ambiguous_request_state", ErrorCode(err))
		}
	})

	t.Run("malformed reservation conflicts", func(t *testing.T) {
		remote := newBareFixtureRemote(t)
		signer, err := NewEphemeralSigner()
		if err != nil {
			t.Fatal(err)
		}
		seedStateBranch(t, remote, StateBranch, map[string]string{"requests/123/789/reservation.json": "not-json\n"})
		store := newFixtureStore(t, remote, signer)
		if _, err := store.LoadState(context.Background(), "123:789"); ErrorCode(err) != "invalid_state_record" {
			t.Fatalf("error = %q, want invalid_state_record", ErrorCode(err))
		}
	})

	t.Run("remote read failure is not absence", func(t *testing.T) {
		remote := newBareFixtureRemote(t)
		signer, err := NewEphemeralSigner()
		if err != nil {
			t.Fatal(err)
		}
		seedStateBranch(t, remote, StateBranch, map[string]string{".keep": "state branch root\n"})
		store := newFixtureStore(t, remote, signer)
		if err := os.RemoveAll(remote); err != nil {
			t.Fatal(err)
		}
		if loaded, err := store.LoadState(context.Background(), "123:789"); err == nil || loaded.Found {
			t.Fatalf("read failure returned found=%t error=%v", loaded.Found, err)
		}
	})
}

// TestGitStateStoreReserveLoadRoundTrip proves the basic reserve -> persist
// -> reload cycle: a fresh request key with no prior state is accepted,
// persisted as a signed, verifiable commit, and reloads with an identical
// reservation, phase, and event chain.
func TestGitStateStoreReserveLoadRoundTrip(t *testing.T) {
	remote := newBareFixtureRemote(t)
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	// Seed an empty-but-existing branch: production treats a missing
	// branch as a provisioning error (S01), so every other test seeds one
	// out-of-band first, mirroring how the real state branch would be
	// provisioned once, outside this wrapper's control.
	seedStateBranch(t, remote, StateBranch, map[string]string{".keep": "state branch root\n"})

	store := newFixtureStore(t, remote, signer)
	ctx := context.Background()

	loaded, err := store.LoadState(ctx, "123:789")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Found {
		t.Fatalf("unexpected existing record: %#v", loaded.Record)
	}

	decision := sealedReservationDecision(t, baseReservation())
	newHead, err := store.PersistReservation(ctx, decision, loaded.RemoteHead)
	if err != nil {
		t.Fatal(err)
	}
	if newHead == loaded.RemoteHead || newHead == "" {
		t.Fatalf("unexpected new head %q (previous %q)", newHead, loaded.RemoteHead)
	}

	reloaded, err := store.LoadState(ctx, "123:789")
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Found {
		t.Fatal("expected to find persisted record")
	}
	if !reflect.DeepEqual(reloaded.Record.Reservation, decision.Record.Reservation) {
		t.Fatalf("reservation = %#v, want %#v", reloaded.Record.Reservation, decision.Record.Reservation)
	}
	if reloaded.Record.Phase != PhaseReserved || len(reloaded.Record.Events) != 2 || reloaded.Record.Events[1].Kind != EventSigningIntent {
		t.Fatalf("unexpected reloaded record: %#v", reloaded.Record)
	}
	if reloaded.RemoteHead != newHead {
		t.Fatalf("remote head = %q, want %q", reloaded.RemoteHead, newHead)
	}
}

// TestGitStateStoreS02OneReservationWinsOtherLosesOrReloads covers §15
// S02: two independent clones attempt to reserve different request keys
// against the same expected parent (simulating a race where both loaded
// state before either wrote). Exactly one push succeeds; the loser's push
// is rejected outright rather than overwriting the winner's record, and a
// subsequent reload of the loser's expected parent shows the winner's
// state, not a merge or silent overwrite.
func TestGitStateStoreS02OneReservationWinsOtherLosesOrReloads(t *testing.T) {
	remote := newBareFixtureRemote(t)
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	seedStateBranch(t, remote, StateBranch, map[string]string{".keep": "state branch root\n"})

	storeA := newFixtureStore(t, remote, signer)
	storeB := newFixtureStore(t, remote, signer)
	ctx := context.Background()

	loadedA, err := storeA.LoadState(ctx, "123:789")
	if err != nil {
		t.Fatal(err)
	}
	loadedB, err := storeB.LoadState(ctx, "123:111")
	if err != nil {
		t.Fatal(err)
	}
	if loadedA.RemoteHead != loadedB.RemoteHead {
		t.Fatalf("expected both clones to observe the same starting head: %q vs %q", loadedA.RemoteHead, loadedB.RemoteHead)
	}

	reservationA := baseReservation()
	decisionA := sealedReservationDecision(t, reservationA)
	headAfterA, err := storeA.PersistReservation(ctx, decisionA, loadedA.RemoteHead)
	if err != nil {
		t.Fatalf("winner PersistReservation: %v", err)
	}

	reservationB := baseReservation()
	reservationB.RequestKey = "123:111"
	reservationB.OriginalCommentID = "111"
	decisionB := sealedReservationDecision(t, reservationB)
	if _, err := storeB.PersistReservation(ctx, decisionB, loadedB.RemoteHead); ErrorCode(err) != "state_conflict" {
		t.Fatalf("loser error = %q, want state_conflict (must reload, not overwrite)", ErrorCode(err))
	}

	// The loser reloads and finds the branch advanced to the winner's
	// head; the winner's record is intact and the loser's request key is
	// still absent, ready to retry against the new head.
	reloaded, err := storeB.LoadState(ctx, "123:789")
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Found || reloaded.RemoteHead != headAfterA {
		t.Fatalf("loser did not observe winner's committed state: %#v", reloaded)
	}
	loserRetry, err := storeB.LoadState(ctx, "123:111")
	if err != nil {
		t.Fatal(err)
	}
	if loserRetry.Found {
		t.Fatal("loser's own request key was incorrectly created by the winner's push")
	}
}

// TestGitStateStoreS02ConcurrentGoroutinesOneReservationWins drives the
// same race as TestGitStateStoreS02OneReservationWinsOtherLosesOrReloads
// but with two goroutines actually racing PersistReservation concurrently
// (via a barrier channel), so `go test -race` exercises real concurrent
// access to the two independent GitStateStore/work-directory instances.
func TestGitStateStoreS02ConcurrentGoroutinesOneReservationWins(t *testing.T) {
	remote := newBareFixtureRemote(t)
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	seedStateBranch(t, remote, StateBranch, map[string]string{".keep": "state branch root\n"})

	storeA := newFixtureStore(t, remote, signer)
	storeB := newFixtureStore(t, remote, signer)
	ctx := context.Background()

	loadedA, err := storeA.LoadState(ctx, "123:789")
	if err != nil {
		t.Fatal(err)
	}
	loadedB, err := storeB.LoadState(ctx, "123:222")
	if err != nil {
		t.Fatal(err)
	}

	reservationA := baseReservation()
	decisionA := sealedReservationDecision(t, reservationA)
	reservationB := baseReservation()
	reservationB.RequestKey, reservationB.OriginalCommentID = "123:222", "222"
	decisionB := sealedReservationDecision(t, reservationB)

	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := storeA.PersistReservation(ctx, decisionA, loadedA.RemoteHead)
		results <- err
	}()
	go func() {
		<-start
		_, err := storeB.PersistReservation(ctx, decisionB, loadedB.RemoteHead)
		results <- err
	}()
	close(start)

	firstErr, secondErr := <-results, <-results
	succeeded, conflicted := 0, 0
	for _, err := range []error{firstErr, secondErr} {
		switch {
		case err == nil:
			succeeded++
		case ErrorCode(err) == "state_conflict":
			conflicted++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("succeeded=%d conflicted=%d, want exactly one of each", succeeded, conflicted)
	}
}

func TestGitStateStoreConcurrentExactRedispatchReservesOnceW06(t *testing.T) {
	remote := newBareFixtureRemote(t)
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	seedStateBranch(t, remote, StateBranch, map[string]string{".keep": "state branch root\n"})
	storeA := newFixtureStore(t, remote, signer)
	storeB := newFixtureStore(t, remote, signer)
	ctx := context.Background()
	loadedA, err := storeA.LoadState(ctx, "123:789")
	if err != nil {
		t.Fatal(err)
	}
	loadedB, err := storeB.LoadState(ctx, "123:789")
	if err != nil {
		t.Fatal(err)
	}
	decision := sealedReservationDecision(t, baseReservation())
	start := make(chan struct{})
	heads := make(chan string, 2)
	errors := make(chan error, 2)
	persist := func(store *GitStateStore, head string) {
		<-start
		result, persistErr := store.PersistReservation(ctx, decision, head)
		heads <- result
		errors <- persistErr
	}
	go persist(storeA, loadedA.RemoteHead)
	go persist(storeB, loadedB.RemoteHead)
	close(start)
	headA, headB := <-heads, <-heads
	errA, errB := <-errors, <-errors
	if errA != nil || errB != nil {
		t.Fatalf("exact redispatch errors: %v, %v", errA, errB)
	}
	if headA == "" || headA != headB {
		t.Fatalf("exact redispatch heads = %q, %q", headA, headB)
	}
	loaded, err := storeA.LoadState(ctx, "123:789")
	if err != nil || !loaded.Found || !reflect.DeepEqual(loaded.Record.Reservation, decision.Record.Reservation) {
		t.Fatalf("final reservation = %#v, %v", loaded, err)
	}
}

// TestGitStateStoreS04IdempotentReservationOnRetry covers §15 S04: a
// record write succeeds but the caller's response is lost (e.g. the
// process crashes right after the push). A retry that reloads state and
// calls ReserveOperation again with the same immutable fields must be
// recognized as success (the existing record), not attempt a conflicting
// second write.
func TestGitStateStoreS04IdempotentReservationOnRetry(t *testing.T) {
	remote := newBareFixtureRemote(t)
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	seedStateBranch(t, remote, StateBranch, map[string]string{".keep": "state branch root\n"})
	store := newFixtureStore(t, remote, signer)
	ctx := context.Background()

	loaded, err := store.LoadState(ctx, "123:789")
	if err != nil {
		t.Fatal(err)
	}
	reservation := baseReservation()
	decision := sealedReservationDecision(t, reservation)
	if _, err := store.PersistReservation(ctx, decision, loaded.RemoteHead); err != nil {
		t.Fatal(err)
	}

	// Simulate "response lost": the caller does not trust its own success
	// and reloads from scratch before deciding what to do next.
	reloaded, err := store.LoadState(ctx, "123:789")
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Found {
		t.Fatal("expected to find the already-persisted record")
	}
	existing := reloaded.Record
	retryDecision, err := ReserveOperation(&existing, reservation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !retryDecision.Reserved || !reflect.DeepEqual(retryDecision.Record.Reservation, reservation) {
		t.Fatalf("retry did not recognize existing reservation as success: %#v", retryDecision)
	}
}

// TestGitStateStoreS06And07ReloadYieldsIdenticalDecisionAfterRunnerLoss
// models §15 S06/S07 at the state.go layer: once a record is persisted at
// a later phase (standing in for "signed" / "branches_published" after a
// runner disappeared), a brand-new store instance (standing in for a new
// runner, since the original clone/work directory is gone) reloads the
// exact same resolved_version/development-equivalent decision rather than
// recomputing it. Full branch-push resumption is out of scope for B06.
func TestGitStateStoreS06And07ReloadYieldsIdenticalDecisionAfterRunnerLoss(t *testing.T) {
	remote := newBareFixtureRemote(t)
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	seedStateBranch(t, remote, StateBranch, map[string]string{".keep": "state branch root\n"})

	firstRunnerStore := newFixtureStore(t, remote, signer)
	ctx := context.Background()
	loaded, err := firstRunnerStore.LoadState(ctx, "123:789")
	if err != nil {
		t.Fatal(err)
	}
	_, output, signResult, generatingWorkDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(ctx, ExecRunner{}, BuildRecoveryBundleInput{WorkDir: generatingWorkDir, SourceSHA: output.SourceSHA, ReleaseSHA: signResult.SignedOutput.ReleaseSHA, Tags: signResult.SignedOutput.Tags, OutputPath: bundlePath})
	if err != nil {
		t.Fatal(err)
	}
	signed := signResult.SignedOutput
	signed.Bundle = bundle
	signed.Bundle.Path = "requests/123/789/recovery.bundle"
	reservation := reservationForSigned("v2.9.0-dev", signed.SourceSHA)
	decision := sealedReservationDecision(t, reservation, signed.SignerFingerprint)
	// Persist a reachable signed state, including its SignedOutput and typed
	// phase evidence, before simulating runner loss.
	signedRecord, alreadySigned, err := AdvanceToSigned(decision.Record, signed, signedRecordEvidence(t, signed))
	if err != nil || alreadySigned {
		t.Fatalf("advance signed: already=%t err=%v", alreadySigned, err)
	}
	decision.Record = signedRecord
	if _, err := firstRunnerStore.PersistReservation(ctx, decision, loaded.RemoteHead); err != nil {
		t.Fatal(err)
	}

	// The "first runner" (its work directory) is gone; a fresh store
	// stands in for a fresh runner picking up the same request key.
	secondRunnerStore := newFixtureStore(t, remote, signer)
	reloaded, err := secondRunnerStore.LoadState(ctx, "123:789")
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Found || reloaded.Record.Phase != PhaseSigned {
		t.Fatalf("second runner did not recover exact recorded phase: %#v", reloaded.Record)
	}
	if reloaded.Record.Reservation.ResolvedVersion != reservation.ResolvedVersion {
		t.Fatalf("resolved version = %q, want %q (must not recompute)", reloaded.Record.Reservation.ResolvedVersion, reservation.ResolvedVersion)
	}
	// Resuming again must not invent a new decision: ReserveOperation
	// against the reloaded record for the same immutable fields returns
	// the identical resolved version, never a new one.
	resumeDecision, err := ReserveOperation(&reloaded.Record, reservation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resumeDecision.Record.Reservation.ResolvedVersion != reservation.ResolvedVersion {
		t.Fatalf("resume recomputed resolved version: got %q, want %q", resumeDecision.Record.Reservation.ResolvedVersion, reservation.ResolvedVersion)
	}
}

// TestGitStateStoreIncompleteOperationsOnLineFiltersByPhaseAndLine proves
// IncompleteOperationsOnLine only returns records matching the requested
// release line and excludes PhaseComplete records, so ReserveOperation's
// V04-style blocking is fed an accurate set.
func TestGitStateStoreIncompleteOperationsOnLineFiltersByPhaseAndLine(t *testing.T) {
	remote := newBareFixtureRemote(t)
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	seedStateBranch(t, remote, StateBranch, map[string]string{".keep": "state branch root\n"})
	store := newFixtureStore(t, remote, signer)
	ctx := context.Background()

	_, output, signResult, generatingWorkDir := bundleFixture(t, "release-v2.9.x", "v2.9.0-rc.1")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(ctx, ExecRunner{}, BuildRecoveryBundleInput{WorkDir: generatingWorkDir, SourceSHA: output.SourceSHA, ReleaseSHA: signResult.SignedOutput.ReleaseSHA, Tags: signResult.SignedOutput.Tags, OutputPath: bundlePath})
	if err != nil {
		t.Fatal(err)
	}
	persist := func(commentID string, phase OperationPhase) {
		reservation := reservationForSigned("v2.9.0-rc.1", signResult.SignedOutput.SourceSHA)
		reservation.RequestKey, reservation.OriginalCommentID = "123:"+commentID, commentID
		loaded, err := store.LoadState(ctx, reservation.RequestKey)
		if err != nil {
			t.Fatal(err)
		}
		decision := sealedReservationDecision(t, reservation)
		signed := signResult.SignedOutput
		signed.Bundle = bundle
		signed.Bundle.Path = "requests/123/" + commentID + "/recovery.bundle"
		decision.Record = advanceFixtureRecordToPhase(t, decision, signed, phase)
		if _, err := store.PersistReservation(ctx, decision, loaded.RemoteHead); err != nil {
			t.Fatalf("persist %s: %v", reservation.RequestKey, err)
		}
	}

	persist("111", PhaseBranchesPublished)
	persist("222", PhaseComplete)

	otherLine := baseReservation()
	otherLine.RequestKey, otherLine.OriginalCommentID = "123:333", "333"
	otherLine.ReleaseLine = "v3.0"
	otherLine.ResolvedVersion = "v3.0.0-rc.1"
	otherLine.GenerationVersion = otherLine.ResolvedVersion
	otherLine.SourceRefs = []SourceRef{{Ref: "refs/heads/release-v3.0.x", SHA: strings.Repeat("b", 40)}}
	otherLine.BranchIntents = []BranchIntent{{Ref: "refs/heads/release-v3.0.x", ExpectedOldSHA: strings.Repeat("b", 40), DesiredSHA: "pending"}}
	loaded, err := store.LoadState(ctx, otherLine.RequestKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PersistReservation(ctx, sealedReservationDecision(t, otherLine), loaded.RemoteHead); err != nil {
		t.Fatal(err)
	}

	records, err := store.IncompleteOperationsOnLine(ctx, "v2.9")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Reservation.RequestKey != "123:111" {
		t.Fatalf("unexpected incomplete records: %#v", records)
	}
}

// TestGitStateStoreRejectsTamperedSignature proves a record whose stored
// signature no longer verifies against the trusted key is rejected on
// load, rather than trusted because the JSON structurally parses. This
// covers the "invalid signature" half of S01's fail-closed requirement.
func TestGitStateStoreRejectsTamperedSignature(t *testing.T) {
	remote := newBareFixtureRemote(t)
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	seedStateBranch(t, remote, StateBranch, map[string]string{".keep": "state branch root\n"})
	store := newFixtureStore(t, remote, signer)
	ctx := context.Background()

	loaded, err := store.LoadState(ctx, "123:789")
	if err != nil {
		t.Fatal(err)
	}
	decision := sealedReservationDecision(t, baseReservation())
	if _, err := store.PersistReservation(ctx, decision, loaded.RemoteHead); err != nil {
		t.Fatal(err)
	}

	// A store trusting a different public key must reject the record: the
	// bytes are well-formed but the signature does not verify.
	untrustedStore := newFixtureStore(t, remote, nil)
	otherSigner, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	untrustedStore.trustedKey = otherSigner.PublicKey()
	if _, err := untrustedStore.LoadState(ctx, "123:789"); ErrorCode(err) != "invalid_state_signature" {
		t.Fatalf("error = %q, want invalid_state_signature", ErrorCode(err))
	}
}

// TestGitStateStoreRejectsUnknownOperationPhase covers the remaining
// fail-closed case from §13.5/§13.6 at the store layer: a persisted
// record whose phase is not one of §13.5's fixed progression values
// blocks loading rather than returning a partially-trusted record. The
// store itself has no way to write such a phase through PersistReservation
// (Go's OperationPhase is just a string), so this test writes the
// malformed document directly to the fixture remote to prove the load
// path's own defense, independent of whether callers are well-behaved.
func TestGitStateStoreRejectsUnknownOperationPhase(t *testing.T) {
	remote := newBareFixtureRemote(t)
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	reservation := baseReservation()
	doc := stateRecordDocument{Reservation: reservation, Phase: OperationPhase("bogus_phase")}
	docBytes, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Seal(signer, docBytes)
	if err != nil {
		t.Fatal(err)
	}
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := StatePaths(reservation.RepositoryID, reservation.OriginalCommentID)
	if err != nil {
		t.Fatal(err)
	}
	seedStateBranch(t, remote, StateBranch, map[string]string{paths.Reservation: string(envelopeBytes)})

	store := newFixtureStore(t, remote, signer)
	if _, err := store.LoadState(context.Background(), reservation.RequestKey); ErrorCode(err) != "unknown_operation_phase" {
		t.Fatalf("error = %q, want unknown_operation_phase", ErrorCode(err))
	}
}
