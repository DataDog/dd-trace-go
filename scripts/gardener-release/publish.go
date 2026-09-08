// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// VerifiedPublication is the capability required by branch and tag
// publication. Its fields are deliberately private: callers can obtain one
// only through VerifyPublication, which checks a remotely loaded signed
// record, its attestation, recovery bundle, and recovered Git objects.
type VerifiedPublication struct {
	record  Record
	workDir string
}

// PublicationVerificationInput binds a remotely loaded operation record to
// the attestation and durable bundle fetched for that same operation.
type PublicationVerificationInput struct {
	Loaded           LoadResult
	Attestation      SignedEnvelope
	TrustedPublicKey []byte
	Verifier         Verifier
	BundlePath       string
	WorkDir          string
}

// VerifyPublication produces the only value accepted by publication. The
// caller must obtain Loaded through StateStore.LoadState; its non-empty remote
// head proves the record came from a verified state-branch snapshot. This
// function then independently verifies the event chain, signed attestation,
// bundle digest/ref/prerequisite set, and all recovered commit/tag objects.
func VerifyPublication(ctx context.Context, runner CommandRunner, input PublicationVerificationInput) (VerifiedPublication, error) {
	if !input.Loaded.Found || !ValidGitObjectID(input.Loaded.RemoteHead) {
		return VerifiedPublication{}, newReleaseError(ErrorClassStateConflict, "signed_record_not_remote")
	}
	record := input.Loaded.Record
	if !phaseAtLeast(record.Phase, PhaseSigned) || record.SignedOutput == nil {
		return VerifiedPublication{}, newReleaseError(ErrorClassStateConflict, "signed_record_required")
	}
	if err := VerifyEventChain(record.Reservation.RequestKey, record.Events); err != nil {
		return VerifiedPublication{}, err
	}
	if record.Reservation.SchemaVersion != "1" || record.Reservation.RequestKey == "" {
		return VerifiedPublication{}, newReleaseError(ErrorClassStateConflict, "invalid_state_record")
	}
	signed := *record.SignedOutput
	if err := validateSignedOutputForPublication(record.Reservation, signed); err != nil {
		return VerifiedPublication{}, err
	}
	if input.Verifier == nil {
		return VerifiedPublication{}, newReleaseError(ErrorClassEvidenceIncomplete, "verifier_unavailable")
	}
	if err := VerifyAttestation(input.Verifier, signed, input.Attestation, input.TrustedPublicKey); err != nil {
		return VerifiedPublication{}, err
	}
	if err := verifyPublicationBundle(ctx, runner, input.WorkDir, input.BundlePath, signed); err != nil {
		return VerifiedPublication{}, err
	}
	return VerifiedPublication{record: record, workDir: input.WorkDir}, nil
}

func validateSignedOutputForPublication(reservation Reservation, signed SignedOutput) error {
	for _, objectID := range []string{signed.UnsignedSHA, signed.SourceSHA, signed.TreeSHA, signed.ReleaseSHA, signed.CommitParentSHA} {
		if !ValidGitObjectID(objectID) {
			return newReleaseError(ErrorClassStateConflict, "invalid_signed_output")
		}
	}
	if signed.SourceSHA != signed.CommitParentSHA || signed.SourceSHA == signed.ReleaseSHA || signed.UnsignedSHA == signed.ReleaseSHA || signed.Bundle.SHA256 == "" || signed.Bundle.SizeBytes <= 0 {
		return newReleaseError(ErrorClassStateConflict, "invalid_signed_output")
	}
	if reservation.ResolvedVersion == "" || len(signed.Tags) == 0 || attestationResolvedVersion(signed) != reservation.ResolvedVersion {
		return newReleaseError(ErrorClassStateConflict, "invalid_signed_output")
	}
	for _, tag := range signed.Tags {
		if !validTagRef(tag) || tag.PeeledCommitSHA != signed.ReleaseSHA {
			return newReleaseError(ErrorClassStateConflict, "invalid_signed_output")
		}
	}
	return nil
}

func verifyPublicationBundle(ctx context.Context, runner CommandRunner, workDir, bundlePath string, signed SignedOutput) error {
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return wrapReleaseError(ErrorClassEvidenceIncomplete, "bundle_read_failed", err)
	}
	if int64(len(data)) != signed.Bundle.SizeBytes {
		return newReleaseError(ErrorClassStateConflict, "bundle_size_mismatch")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != signed.Bundle.SHA256 {
		return newReleaseError(ErrorClassStateConflict, "bundle_digest_mismatch")
	}
	expectedRefs := map[string]string{"refs/heads/gardener-release-bundle-tip": signed.ReleaseSHA}
	for _, tag := range signed.Tags {
		expectedRefs[tag.Ref] = tag.TagObjectSHA
	}
	if _, err := verifyBundleContents(ctx, runner, workDir, bundlePath, expectedRefs, signed.Bundle.PrerequisiteSHAs); err != nil {
		return err
	}
	if err := verifyRecoveredCommit(ctx, runner, workDir, signed.ReleaseSHA, signed.TreeSHA); err != nil {
		return err
	}
	for _, tag := range signed.Tags {
		if err := verifyRecoveredTag(ctx, runner, workDir, tag); err != nil {
			return err
		}
	}
	return nil
}

// VerifiedTestEvidence is the structural output B10's exact-SHA test gate
// supplies after semantic verification. B09 does not decide which workflows
// or jobs count; it only refuses tag publication unless evidence is
// schema-bound to this exact release SHA and carries a fixed digest.
type VerifiedTestEvidence struct {
	SchemaVersion  string `json:"schema_version"`
	ReleaseSHA     string `json:"release_sha"`
	EvidenceSHA256 string `json:"evidence_sha256"`
}

func validateTestEvidence(evidence VerifiedTestEvidence, releaseSHA string) error {
	if evidence.SchemaVersion != "1" || evidence.ReleaseSHA != releaseSHA || !lowerHexDigest(evidence.EvidenceSHA256) {
		return newReleaseError(ErrorClassTestGateFailed, "verified_test_evidence_required")
	}
	return nil
}

func lowerHexDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// DecodeVerifiedTestEvidenceJSON strictly decodes B10's future evidence
// handoff. Unknown, duplicate, missing, null, and wrong-typed fields fail
// closed before any tag mutation.
func DecodeVerifiedTestEvidenceJSON(raw []byte) (VerifiedTestEvidence, error) {
	fields, err := decodeStrictObject(raw, MaxContextBytes, ErrInvalidContextJSON, ErrTrailingContextJSON, ErrDuplicateContextKey)
	if err != nil {
		return VerifiedTestEvidence{}, newReleaseError(ErrorClassTestGateFailed, "invalid_test_evidence")
	}
	allowed := map[string]bool{"schema_version": true, "release_sha": true, "evidence_sha256": true}
	for key := range fields {
		if !allowed[key] {
			return VerifiedTestEvidence{}, newReleaseError(ErrorClassTestGateFailed, "invalid_test_evidence")
		}
	}
	read := func(key string) (string, error) {
		value, err := stringField(fields, key, newReleaseError(ErrorClassTestGateFailed, "invalid_test_evidence"), newReleaseError(ErrorClassTestGateFailed, "invalid_test_evidence"))
		return value, err
	}
	var evidence VerifiedTestEvidence
	if evidence.SchemaVersion, err = read("schema_version"); err != nil {
		return VerifiedTestEvidence{}, err
	}
	if evidence.ReleaseSHA, err = read("release_sha"); err != nil {
		return VerifiedTestEvidence{}, err
	}
	if evidence.EvidenceSHA256, err = read("evidence_sha256"); err != nil {
		return VerifiedTestEvidence{}, err
	}
	return evidence, nil
}

// RefState is an exact remote-ref classification made before a mutation.
type RefState string

const (
	RefAbsent      RefState = "absent"
	RefExpectedOld RefState = "expected_old"
	RefDesired     RefState = "desired"
	RefConflict    RefState = "conflict"
)

// PublicationResult returns the updated append-only record. The caller
// persists it through GitStateStore.PersistReservation using the state head it
// loaded before invoking publication.
type PublicationResult struct {
	Record         Record
	PublishedRefs  []string
	ReconciledRefs []string
}

// PublishBranches reconciles branch intents in recorded order. Prepare
// records must contain release then development create-only intents, which
// enforces release-at-source before development-at-release. Promote/release
// records carry one fast-forward update intent. A push response is never
// trusted: the exact ref is re-read after every attempt, including errors.
func PublishBranches(ctx context.Context, runner CommandRunner, verified VerifiedPublication, remotePath string) (PublicationResult, error) {
	if err := validatePublicationRemote(remotePath); err != nil {
		return PublicationResult{}, err
	}
	record := verified.record
	if !phaseAtLeast(record.Phase, PhaseSigned) || record.SignedOutput == nil {
		return PublicationResult{}, newReleaseError(ErrorClassStateConflict, "signed_record_required")
	}
	if err := validateBranchIntents(record); err != nil {
		return PublicationResult{}, err
	}
	result := PublicationResult{Record: record}
	for _, intent := range record.Reservation.BranchIntents {
		state, err := classifyBranchRef(ctx, runner, verified.workDir, remotePath, intent)
		if err != nil {
			return result, err
		}
		if state == RefDesired {
			result.ReconciledRefs = append(result.ReconciledRefs, intent.Ref)
			if record.Phase == PhaseSigned {
				if err := appendRefEvent(&result.Record, EventBranchPublished, intent.Ref, intent.DesiredSHA, "reconciled"); err != nil {
					return result, err
				}
			}
			continue
		}
		// Once the durable record says branches_published or later, every
		// branch must still reconcile to its desired object. Missing or
		// moved refs are operator conflicts; a resume must never recreate or
		// update them after the publication phase has already advanced.
		if record.Phase != PhaseSigned {
			return result, newReleaseError(ErrorClassStateConflict, "recorded_branch_ref_mismatch")
		}
		if state == RefConflict || (intent.ExpectedOldSHA == "" && state != RefAbsent) || (intent.ExpectedOldSHA != "" && state != RefExpectedOld) {
			return result, newReleaseError(ErrorClassStateConflict, "branch_ref_conflict")
		}
		pushErr := pushBranch(ctx, runner, verified.workDir, remotePath, intent)
		postState, readErr := classifyBranchRef(ctx, runner, verified.workDir, remotePath, intent)
		if readErr != nil {
			return result, readErr
		}
		if postState != RefDesired {
			if postState == RefConflict {
				return result, newReleaseError(ErrorClassStateConflict, "branch_ref_conflict")
			}
			if pushErr != nil {
				return result, wrapReleaseError(ErrorClassPublicationPartial, "branch_push_unconfirmed", pushErr)
			}
			return result, newReleaseError(ErrorClassPublicationPartial, "branch_push_unconfirmed")
		}
		result.PublishedRefs = append(result.PublishedRefs, intent.Ref)
		if err := appendRefEvent(&result.Record, EventBranchPublished, intent.Ref, intent.DesiredSHA, "published"); err != nil {
			return result, err
		}
	}
	if record.Phase == PhaseSigned {
		if err := advancePublicationPhase(&result.Record, PhaseBranchesPublished); err != nil {
			return result, err
		}
	}
	return result, nil
}

func validateBranchIntents(record Record) error {
	line, err := parseReleaseLine(record.Reservation.ReleaseLine)
	if err != nil {
		return newReleaseError(ErrorClassStateConflict, "invalid_branch_intent")
	}
	intents := record.Reservation.BranchIntents
	signed := record.SignedOutput
	switch record.Reservation.Command {
	case "release:prepare":
		if len(intents) != 2 {
			return newReleaseError(ErrorClassStateConflict, "invalid_branch_intent")
		}
		want := []BranchIntent{
			{Ref: "refs/heads/" + releaseBranchName(line.Major, line.Minor), DesiredSHA: signed.SourceSHA},
			{Ref: "refs/heads/" + devBranchName(line.Major, line.Minor+1), DesiredSHA: signed.ReleaseSHA},
		}
		for i := range intents {
			if intents[i].Ref != want[i].Ref || intents[i].ExpectedOldSHA != "" || intents[i].DesiredSHA != want[i].DesiredSHA {
				return newReleaseError(ErrorClassStateConflict, "invalid_branch_intent")
			}
		}
	case "release:promote", "release:release":
		if len(intents) != 1 || intents[0].Ref != "refs/heads/"+releaseBranchName(line.Major, line.Minor) || !ValidGitObjectID(intents[0].ExpectedOldSHA) || intents[0].DesiredSHA != signed.ReleaseSHA {
			return newReleaseError(ErrorClassStateConflict, "invalid_branch_intent")
		}
	default:
		return newReleaseError(ErrorClassStateConflict, "invalid_branch_intent")
	}
	for _, intent := range intents {
		if !validBranchRef(intent.Ref) || !ValidGitObjectID(intent.DesiredSHA) {
			return newReleaseError(ErrorClassStateConflict, "invalid_branch_intent")
		}
	}
	return nil
}

var branchRefPattern = regexp.MustCompile(`^refs/heads/(?:release|dev)-v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.x$`)

func validBranchRef(ref string) bool { return branchRefPattern.MatchString(ref) }

func classifyBranchRef(ctx context.Context, runner CommandRunner, workDir, remotePath string, intent BranchIntent) (RefState, error) {
	sha, found, err := readRemoteRef(ctx, runner, workDir, remotePath, intent.Ref)
	if err != nil {
		return RefConflict, err
	}
	if !found {
		return RefAbsent, nil
	}
	if sha == intent.DesiredSHA {
		return RefDesired, nil
	}
	if intent.ExpectedOldSHA != "" && sha == intent.ExpectedOldSHA {
		return RefExpectedOld, nil
	}
	return RefConflict, nil
}

func pushBranch(ctx context.Context, runner CommandRunner, workDir, remotePath string, intent BranchIntent) error {
	args := []string{"-c", "protocol.file.allow=always", "-c", "core.hooksPath=/dev/null", "-c", "push.followTags=false", "push", "--no-follow-tags"}
	if intent.ExpectedOldSHA == "" {
		args = append(args, "--force-with-lease="+intent.Ref+":")
	}
	args = append(args, remotePath, intent.DesiredSHA+":"+intent.Ref)
	command := Command{Path: "git", Args: args, Env: gitStateEnv(), Dir: workDir}
	if err := ValidatePushCommand(command); err != nil {
		return err
	}
	_, err := runner.Run(ctx, command)
	return err
}

// PublishTags publishes one fully-qualified annotated tag ref at a time in
// SignedOutput.Tags order. It requires both a tests_passed record and B10's
// structurally verified evidence before the first tag mutation.
func PublishTags(ctx context.Context, runner CommandRunner, verified VerifiedPublication, remotePath string, evidence VerifiedTestEvidence) (PublicationResult, error) {
	if err := validatePublicationRemote(remotePath); err != nil {
		return PublicationResult{}, err
	}
	record := verified.record
	if record.SignedOutput == nil {
		return PublicationResult{}, newReleaseError(ErrorClassStateConflict, "signed_record_required")
	}
	if err := validateTestEvidence(evidence, record.SignedOutput.ReleaseSHA); err != nil {
		return PublicationResult{}, err
	}
	if !phaseAtLeast(record.Phase, PhaseTestsPassed) {
		return PublicationResult{}, newReleaseError(ErrorClassTestGateFailed, "tests_passed_phase_required")
	}
	result := PublicationResult{Record: record}
	for _, tag := range record.SignedOutput.Tags {
		if !validTagRef(tag) || tag.PeeledCommitSHA != record.SignedOutput.ReleaseSHA {
			return result, newReleaseError(ErrorClassStateConflict, "invalid_tag_intent")
		}
		state, err := classifyTagRef(ctx, runner, verified.workDir, remotePath, tag)
		if err != nil {
			return result, err
		}
		if state == RefDesired {
			result.ReconciledRefs = append(result.ReconciledRefs, tag.Ref)
			if record.Phase == PhaseTestsPassed {
				if err := appendRefEvent(&result.Record, EventTagPublished, tag.Ref, tag.TagObjectSHA, "reconciled"); err != nil {
					return result, err
				}
			}
			continue
		}
		// tags_published and later phases are verify-only. A missing or
		// changed recorded tag is a state conflict, not permission to
		// recreate immutable release evidence.
		if record.Phase != PhaseTestsPassed {
			return result, newReleaseError(ErrorClassStateConflict, "recorded_tag_ref_mismatch")
		}
		if state != RefAbsent {
			return result, newReleaseError(ErrorClassStateConflict, "tag_ref_conflict")
		}
		pushErr := pushTag(ctx, runner, verified.workDir, remotePath, tag)
		postState, readErr := classifyTagRef(ctx, runner, verified.workDir, remotePath, tag)
		if readErr != nil {
			return result, readErr
		}
		if postState != RefDesired {
			if postState == RefConflict {
				return result, newReleaseError(ErrorClassStateConflict, "tag_ref_conflict")
			}
			if pushErr != nil {
				return result, wrapReleaseError(ErrorClassPublicationPartial, "tag_push_unconfirmed", pushErr)
			}
			return result, newReleaseError(ErrorClassPublicationPartial, "tag_push_unconfirmed")
		}
		result.PublishedRefs = append(result.PublishedRefs, tag.Ref)
		if err := appendRefEvent(&result.Record, EventTagPublished, tag.Ref, tag.TagObjectSHA, "published"); err != nil {
			return result, err
		}
	}
	if record.Phase == PhaseTestsPassed {
		if err := advancePublicationPhase(&result.Record, PhaseTagsPublished); err != nil {
			return result, err
		}
	}
	return result, nil
}

func validTagRef(tag TagRef) bool {
	return tag.Name != "" && tag.Ref == "refs/tags/"+tag.Name && validFullTagRef(tag.Ref) && ValidGitObjectID(tag.TagObjectSHA) && ValidGitObjectID(tag.PeeledCommitSHA) && tag.TagObjectSHA != tag.PeeledCommitSHA
}

func validFullTagRef(ref string) bool {
	if !strings.HasPrefix(ref, "refs/tags/") || len(ref) > 512 || strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") || strings.ContainsAny(ref, " ~^:?*[\\") {
		return false
	}
	return !strings.Contains(ref, "//")
}

func classifyTagRef(ctx context.Context, runner CommandRunner, workDir, remotePath string, tag TagRef) (RefState, error) {
	result, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=always", "-c", "core.hooksPath=/dev/null", "ls-remote", remotePath, tag.Ref, tag.Ref + "^{}"},
		Env:  gitStateEnv(),
		Dir:  workDir,
	})
	if err != nil {
		return RefConflict, wrapReleaseError(ErrorClassEvidenceIncomplete, "remote_ref_read_failed", err)
	}
	objectSHA, peeledSHA := "", ""
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !ValidGitObjectID(fields[0]) {
			return RefConflict, newReleaseError(ErrorClassEvidenceIncomplete, "remote_ref_unparseable")
		}
		switch fields[1] {
		case tag.Ref:
			objectSHA = fields[0]
		case tag.Ref + "^{}":
			peeledSHA = fields[0]
		default:
			return RefConflict, newReleaseError(ErrorClassEvidenceIncomplete, "remote_ref_unparseable")
		}
	}
	if objectSHA == "" && peeledSHA == "" {
		return RefAbsent, nil
	}
	if objectSHA == tag.TagObjectSHA && peeledSHA == tag.PeeledCommitSHA {
		return RefDesired, nil
	}
	return RefConflict, nil
}

func pushTag(ctx context.Context, runner CommandRunner, workDir, remotePath string, tag TagRef) error {
	command := Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=always", "-c", "core.hooksPath=/dev/null", "-c", "push.followTags=false", "push", "--no-atomic", "--no-follow-tags", remotePath, tag.Ref + ":" + tag.Ref},
		Env:  gitStateEnv(),
		Dir:  workDir,
	}
	if err := ValidatePushCommand(command); err != nil {
		return err
	}
	_, err := runner.Run(ctx, command)
	return err
}

func readRemoteRef(ctx context.Context, runner CommandRunner, workDir, remotePath, ref string) (string, bool, error) {
	result, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=always", "-c", "core.hooksPath=/dev/null", "ls-remote", "--refs", remotePath, ref},
		Env:  gitStateEnv(),
		Dir:  workDir,
	})
	if err != nil {
		return "", false, wrapReleaseError(ErrorClassEvidenceIncomplete, "remote_ref_read_failed", err)
	}
	trimmed := strings.TrimSpace(result.Stdout)
	if trimmed == "" {
		return "", false, nil
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) != 1 {
		return "", false, newReleaseError(ErrorClassEvidenceIncomplete, "remote_ref_unparseable")
	}
	fields := strings.Fields(lines[0])
	if len(fields) != 2 || fields[1] != ref || !ValidGitObjectID(fields[0]) {
		return "", false, newReleaseError(ErrorClassEvidenceIncomplete, "remote_ref_unparseable")
	}
	return fields[0], true, nil
}

func validatePublicationRemote(remotePath string) error {
	if remotePath == "" || !filepath.IsAbs(remotePath) || strings.Contains(remotePath, "://") {
		return newReleaseError(ErrorClassContractMismatch, "invalid_publication_remote")
	}
	info, err := os.Stat(remotePath)
	if err != nil || !info.IsDir() {
		return newReleaseError(ErrorClassContractMismatch, "invalid_publication_remote")
	}
	return nil
}

func phaseAtLeast(actual, minimum OperationPhase) bool {
	actualRank, actualOK := phaseOrder[actual]
	minimumRank, minimumOK := phaseOrder[minimum]
	return actualOK && minimumOK && actualRank >= minimumRank
}

func appendRefEvent(record *Record, kind EventKind, ref, objectSHA, disposition string) error {
	if hasRefEvent(record.Events, kind, ref, objectSHA) {
		return nil
	}
	evidence, err := json.Marshal(struct {
		Ref         string `json:"ref"`
		ObjectSHA   string `json:"object_sha"`
		Disposition string `json:"disposition"`
	}{Ref: ref, ObjectSHA: objectSHA, Disposition: disposition})
	if err != nil {
		return wrapReleaseError(ErrorClassStateConflict, "event_marshal_failed", err)
	}
	events, err := AppendEvent(record.Events, record.Reservation.RequestKey, kind, evidence)
	if err != nil {
		return err
	}
	record.Events = events
	return nil
}

func hasRefEvent(events []Event, kind EventKind, ref, objectSHA string) bool {
	for _, event := range events {
		if event.Kind != kind {
			continue
		}
		var evidence struct {
			Ref       string `json:"ref"`
			ObjectSHA string `json:"object_sha"`
		}
		if json.Unmarshal(event.Evidence, &evidence) == nil && evidence.Ref == ref && evidence.ObjectSHA == objectSHA {
			return true
		}
	}
	return false
}

func advancePublicationPhase(record *Record, next OperationPhase) error {
	phase, err := AdvancePhase(record.Phase, next)
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(struct {
		Phase OperationPhase `json:"phase"`
	}{Phase: phase})
	if err != nil {
		return wrapReleaseError(ErrorClassStateConflict, "event_marshal_failed", err)
	}
	events, err := AppendEvent(record.Events, record.Reservation.RequestKey, EventPhaseAdvanced, evidence)
	if err != nil {
		return err
	}
	record.Phase = phase
	record.Events = events
	return nil
}

// ValidatePushCommand is a defense-in-depth assertion used by tests and
// future orchestration before execution. It rejects every disallowed push
// shape, including deletion refspecs, bulk pushes, bare force, implicit or
// non-empty leases, and leases on tags/update paths.
func ValidatePushCommand(command Command) error {
	if command.Path != "git" {
		return newReleaseError(ErrorClassContractMismatch, "invalid_push_command")
	}
	pushIndex := -1
	for i, arg := range command.Args {
		if arg == "push" {
			pushIndex = i
			break
		}
	}
	if pushIndex < 0 {
		return newReleaseError(ErrorClassContractMismatch, "invalid_push_command")
	}
	leaseRef := ""
	for _, arg := range command.Args[pushIndex+1:] {
		switch arg {
		case "--force", "-f", "--force-if-includes", "--delete", "-d", "--mirror", "--all", "--tags":
			return newReleaseError(ErrorClassContractMismatch, "unsafe_push_command")
		}
		if strings.HasPrefix(arg, "--force-with-lease") {
			parts := strings.SplitN(arg, "=", 2)
			if leaseRef != "" || len(parts) != 2 || !strings.HasPrefix(parts[1], "refs/heads/") || !strings.HasSuffix(parts[1], ":") || strings.Count(parts[1], ":") != 1 {
				return newReleaseError(ErrorClassContractMismatch, "unsafe_push_command")
			}
			leaseRef = strings.TrimSuffix(parts[1], ":")
		}
		if strings.HasPrefix(arg, ":refs/") || strings.HasPrefix(arg, "+") {
			return newReleaseError(ErrorClassContractMismatch, "unsafe_push_command")
		}
	}
	if leaseRef != "" {
		refspec := command.Args[len(command.Args)-1]
		parts := strings.Split(refspec, ":")
		if len(parts) != 2 || !ValidGitObjectID(parts[0]) || parts[1] != leaseRef || !validBranchRef(leaseRef) {
			return newReleaseError(ErrorClassContractMismatch, "unsafe_push_command")
		}
	}
	return nil
}
