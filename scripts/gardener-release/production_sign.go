// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// ProductionSignInput contains only protected-job artifacts and reviewed policy.
type ProductionSignInput struct {
	Record             Record
	ExpectedStateHead  string
	Generation         GenerationOutput
	Manifest           PlanManifest
	GenerationBundle   string
	GenerationSHA256   string
	ToolPath           string
	ValidatorPath      string
	Policy             Policy
	GenerationEvidence JobArtifact
}

// ProductionSignResult is the durable state snapshot after protected signing.
type ProductionSignResult struct {
	Record       Record       `json:"record"`
	StateHead    string       `json:"state_head"`
	SignedOutput SignedOutput `json:"signed_output"`
}

// ValidateSignAndStoreProduction reconstructs an unsigned artifact in a fresh
// repository, revalidates it, signs the commit and tags with SSH, and stores the
// recovery bundle atomically with the signed state transition.
func ValidateSignAndStoreProduction(ctx context.Context, input ProductionSignInput) (result ProductionSignResult, err error) {
	if !ValidGitObjectID(input.ExpectedStateHead) || input.Record.Phase != PhaseReserved || len(input.Record.Reservation.SourceRefs) != 1 || input.Record.Reservation.RequestKey != input.GenerationEvidence.RequestKey || input.GenerationEvidence.Job != JobGenerateUnsigned {
		return result, newReleaseError(ErrorClassStateConflict, "signing_input_mismatch")
	}
	if input.Generation.SourceSHA != input.Record.Reservation.SourceRefs[0].SHA || input.Generation.ResolvedVersion != input.Record.Reservation.GenerationVersion {
		return result, newReleaseError(ErrorClassStateConflict, "generation_reservation_mismatch")
	}
	toolDigest, err := boundedFileSHA256(input.ToolPath)
	if err != nil {
		return result, err
	}
	validatorDigest, err := boundedFileSHA256(input.ValidatorPath)
	if err != nil {
		return result, err
	}
	credentials, err := LoadProtectedGitHubGitCredentials()
	if err != nil {
		return result, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, credentials.Close()) }()
	gitSigning, err := LoadProtectedSSHSigningKey(input.Policy.Signing)
	if err != nil {
		return result, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, gitSigning.Close()) }()
	contentSigner, err := NewSSHContentSigner(gitSigning)
	if err != nil {
		return result, err
	}
	runner := ProtectedGitRunner{Runner: ExecRunner{}, Credentials: credentials}
	stateDir, err := osMkdirPrivateTemp("gardener-release-state-")
	if err != nil {
		return result, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, removePrivateTemp(stateDir)) }()
	store, err := NewProtectedGitStateStore(runner, stateDir, contentSigner, contentSigner, mustSSHWire(contentSigner), CommitIdentity{Name: "gardener-release", Email: input.Policy.Signing.Principal}, RealClock{}, gitSigning)
	if err != nil {
		return result, err
	}
	if err := store.InitWorkingRepository(ctx); err != nil {
		return result, err
	}
	loaded, err := store.LoadState(ctx, input.Record.Reservation.RequestKey)
	if err != nil {
		return result, err
	}
	if !loaded.Found || loaded.RemoteHead != input.ExpectedStateHead || !recordsEqual(loaded.Record, input.Record) {
		return result, newReleaseError(ErrorClassStateConflict, "state_changed_before_signing")
	}
	workDir, err := osMkdirPrivateTemp("gardener-release-sign-")
	if err != nil {
		return result, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, removePrivateTemp(workDir)) }()
	if _, err := runner.Run(ctx, Command{Path: "git", Args: []string{"init", "--quiet", "-b", "sign"}, Env: gitStateEnv(), Dir: workDir}); err != nil {
		return result, wrapReleaseError(ErrorClassEvidenceIncomplete, "sign_repository_init_failed", err)
	}
	sourceSHA := input.Record.Reservation.SourceRefs[0].SHA
	if _, err := runner.Run(ctx, Command{Path: "git", Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "fetch", "--quiet", "--no-tags", CanonicalGitHubRemote, sourceSHA}, Env: gitStateEnv(), Dir: workDir}); err != nil {
		return result, wrapReleaseError(ErrorClassEvidenceIncomplete, "sign_source_fetch_failed", err)
	}
	actions := protectedSignStoreActions{
		sign: func(trustedManifest PlanManifest) (SignedCommitResult, error) {
			intent, err := signingIntentFromRecord(input.Record)
			if err != nil {
				return SignedCommitResult{}, err
			}
			reader := GitBlobReader{Runner: runner, Dir: workDir}
			return SignCommitWithSSH(ctx, runner, RealClock{}, contentSigner, SignInput{Output: input.Generation, Manifest: trustedManifest, Reader: reader.Read, WorkDir: workDir, ToolDigest: toolDigest, ValidatorDigest: validatorDigest, SigningIntent: &intent}, gitSigning)
		},
		persist: func(signed SignedCommitResult) (ProductionSignResult, error) {
			paths, err := StatePaths(input.Record.Reservation.RepositoryID, input.Record.Reservation.OriginalCommentID)
			if err != nil {
				return ProductionSignResult{}, err
			}
			recoveryPath := filepath.Join(workDir, "recovery.bundle")
			bundle, err := BuildRecoveryBundle(ctx, runner, BuildRecoveryBundleInput{WorkDir: workDir, SourceSHA: signed.SignedOutput.SourceSHA, ReleaseSHA: signed.SignedOutput.ReleaseSHA, Tags: signed.SignedOutput.Tags, OutputPath: recoveryPath})
			if err != nil {
				return ProductionSignResult{}, err
			}
			bundle.Path = paths.RecoveryBundle
			signed.SignedOutput.Bundle = bundle
			evidence, err := json.Marshal(signedPhaseEvidence{Phase: PhaseSigned, ReleaseSHA: signed.SignedOutput.ReleaseSHA, GenerationArtifactSHA256: input.GenerationEvidence.ArtifactSHA256, WorkflowRunID: input.GenerationEvidence.WorkflowRunID, WorkflowRunAttempt: input.GenerationEvidence.WorkflowAttempt})
			if err != nil {
				return ProductionSignResult{}, wrapReleaseError(ErrorClassStateConflict, "signing_evidence_failed", err)
			}
			record, alreadySigned, err := AdvanceToSigned(input.Record, signed.SignedOutput, evidence)
			if err != nil {
				return ProductionSignResult{}, err
			}
			if alreadySigned {
				return ProductionSignResult{}, newReleaseError(ErrorClassStateConflict, "unexpected_already_signed")
			}
			bundleData, err := os.ReadFile(recoveryPath)
			if err != nil {
				return ProductionSignResult{}, wrapReleaseError(ErrorClassEvidenceIncomplete, "bundle_read_failed", err)
			}
			head, err := store.PersistSignedRecord(ctx, ReservationDecision{Record: record}, input.ExpectedStateHead, signed.Attestation, bundleData)
			if err != nil {
				return ProductionSignResult{}, err
			}
			return ProductionSignResult{Record: record, StateHead: head, SignedOutput: signed.SignedOutput}, nil
		},
	}
	return validateProtectedGenerationAndStore(ctx, runner, workDir, input.GenerationBundle, input.GenerationSHA256, input.ToolPath, input.Generation, input.Manifest, actions)
}

type protectedSignStoreActions struct {
	sign    func(PlanManifest) (SignedCommitResult, error)
	persist func(SignedCommitResult) (ProductionSignResult, error)
}

func validateProtectedGenerationAndStore(ctx context.Context, runner CommandRunner, workDir, bundlePath, bundleDigest, taggerPath string, generation GenerationOutput, claimedManifest PlanManifest, actions protectedSignStoreActions) (ProductionSignResult, error) {
	if actions.sign == nil || actions.persist == nil {
		return ProductionSignResult{}, newReleaseError(ErrorClassContractMismatch, "signing_actions_unavailable")
	}
	if err := RestoreUnsignedRepositoryBundle(ctx, runner, workDir, bundlePath, bundleDigest, generation); err != nil {
		return ProductionSignResult{}, err
	}
	trustedManifest, err := rederiveProtectedGeneration(ctx, runner, workDir, taggerPath, generation)
	if err != nil {
		return ProductionSignResult{}, err
	}
	if !reflect.DeepEqual(trustedManifest, claimedManifest) {
		return ProductionSignResult{}, newReleaseError(ErrorClassGenerationFailed, "generation_manifest_claim_mismatch")
	}
	signed, err := actions.sign(trustedManifest)
	if err != nil {
		return ProductionSignResult{}, err
	}
	return actions.persist(signed)
}

func rederiveProtectedGeneration(ctx context.Context, runner CommandRunner, workDir, taggerPath string, claimed GenerationOutput) (PlanManifest, error) {
	g := generator{ctx: ctx, runner: runner, workDir: workDir}
	objectType, err := g.trimmedOutput("cat-file", "-t", claimed.CommitSHA)
	if err != nil || objectType != "commit" {
		return PlanManifest{}, newReleaseError(ErrorClassGenerationFailed, "generation_commit_object_invalid")
	}
	tree, err := g.trimmedOutput("rev-parse", claimed.CommitSHA+"^{tree}")
	if err != nil {
		return PlanManifest{}, err
	}
	parents, err := g.commitParents(claimed.CommitSHA)
	if err != nil {
		return PlanManifest{}, err
	}
	changes, err := g.diffTree(claimed.SourceSHA, claimed.CommitSHA)
	if err != nil {
		return PlanManifest{}, err
	}
	if tree != claimed.TreeSHA || !reflect.DeepEqual(parents, claimed.ParentSHAs) || !reflect.DeepEqual(changes, claimed.Changes) {
		return PlanManifest{}, newReleaseError(ErrorClassGenerationFailed, "generation_git_claim_mismatch")
	}
	commit, err := g.trimmedOutput("cat-file", "-p", claimed.CommitSHA)
	if err != nil {
		return PlanManifest{}, err
	}
	identity := generationCommitterName + " <" + generationCommitterEmail + ">"
	if strings.Contains(commit, "\ngpgsig ") || !strings.Contains(commit, "\nauthor "+identity+" ") || !strings.Contains(commit, "\ncommitter "+identity+" ") || !strings.HasSuffix(commit, "\n\ninternal/version: "+claimed.ResolvedVersion) {
		return PlanManifest{}, newReleaseError(ErrorClassGenerationFailed, "generation_commit_metadata_invalid")
	}
	if _, err := g.run("checkout", "--quiet", "-B", claimed.Branch, claimed.SourceSHA); err != nil {
		return PlanManifest{}, err
	}
	planPath := filepath.Join(workDir, ".gardener-release-trusted-plan.json")
	args := []string{"--root", workDir, "--version", claimed.ResolvedVersion, "--plan-json", planPath, "--untag-modules", "github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2", "--exclude-dirs", "_tools,.claude,.github,tools"}
	if _, err := runner.Run(ctx, Command{Path: taggerPath, Args: args, Env: taggerGenerationEnv(), Dir: workDir}); err != nil {
		return PlanManifest{}, wrapReleaseError(ErrorClassGenerationFailed, "trusted_plan_failed", err)
	}
	raw, err := os.ReadFile(planPath)
	if err != nil || validateJSONNoDuplicateKeys(raw, MaxJobArtifactBytes) != nil {
		return PlanManifest{}, newReleaseError(ErrorClassGenerationFailed, "trusted_plan_invalid")
	}
	var trustedRaw map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&trustedRaw) != nil {
		return PlanManifest{}, newReleaseError(ErrorClassGenerationFailed, "trusted_plan_invalid")
	}
	trusted, err := PlanManifestFromTaggerOutput(trustedRaw)
	if err != nil {
		return PlanManifest{}, err
	}
	claimedManifest, err := PlanManifestFromTaggerOutput(claimed.TaggerManifest)
	if err != nil {
		return PlanManifest{}, err
	}
	trustedJSON, _ := canonicalJSON(trustedRaw)
	claimedJSON, _ := canonicalJSON(claimed.TaggerManifest)
	if string(trustedJSON) != string(claimedJSON) || !reflect.DeepEqual(trusted, claimedManifest) {
		return PlanManifest{}, newReleaseError(ErrorClassGenerationFailed, "generation_manifest_claim_mismatch")
	}
	return trusted, nil
}

func signingIntentFromRecord(record Record) (GitSigningIntent, error) {
	var intent GitSigningIntent
	found := false
	for _, event := range record.Events {
		if event.Kind != EventSigningIntent {
			continue
		}
		var current GitSigningIntent
		decoder := json.NewDecoder(bytes.NewReader(event.Evidence))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&current) != nil || found {
			return GitSigningIntent{}, newReleaseError(ErrorClassStateConflict, "signing_intent_conflict")
		}
		intent, found = current, true
	}
	if !found || intent.Message != "release: "+record.Reservation.GenerationVersion {
		return GitSigningIntent{}, newReleaseError(ErrorClassStateConflict, "signing_intent_required")
	}
	return intent, nil
}

func recordsEqual(left, right Record) bool {
	leftJSON, leftErr := canonicalJSON(left)
	rightJSON, rightErr := canonicalJSON(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func boundedFileSHA256(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxRecoveryBundleBytes {
		return "", newReleaseError(ErrorClassEvidenceIncomplete, "tool_artifact_invalid")
	}
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) != info.Size() {
		return "", newReleaseError(ErrorClassEvidenceIncomplete, "tool_artifact_invalid")
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
