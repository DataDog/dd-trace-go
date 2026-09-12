// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"os"
	"path/filepath"
)

// ProductionPublicationInput binds one cross-job record to one remote state head.
type ProductionPublicationInput struct {
	Record            Record
	ExpectedStateHead string
	Policy            Policy
}

// ProductionPublicationResult is returned by branch and tag publication jobs.
type ProductionPublicationResult struct {
	Record      Record            `json:"record"`
	StateHead   string            `json:"state_head"`
	Publication PublicationResult `json:"publication"`
}

type productionPublicationOperation func(context.Context, CommandRunner, VerifiedPublication, *GitStateStore, LoadResult) (ProductionPublicationResult, error)

func withVerifiedProductionPublication(ctx context.Context, input ProductionPublicationInput, operation productionPublicationOperation) (result ProductionPublicationResult, err error) {
	if operation == nil || !ValidGitObjectID(input.ExpectedStateHead) || input.Record.SignedOutput == nil {
		return result, newReleaseError(ErrorClassStateConflict, "publication_input_invalid")
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
		return result, newReleaseError(ErrorClassStateConflict, "state_changed_before_publication")
	}
	material, err := store.LoadSignedStateMaterial(ctx, input.Record.Reservation.RequestKey, loaded.RemoteHead)
	if err != nil {
		return result, err
	}
	workDir, err := osMkdirPrivateTemp("gardener-release-publish-")
	if err != nil {
		return result, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, removePrivateTemp(workDir)) }()
	bundlePath := filepath.Join(workDir, "recovery.bundle")
	if err := os.WriteFile(bundlePath, material.Bundle, 0600); err != nil {
		return result, newReleaseError(ErrorClassEvidenceIncomplete, "bundle_materialize_failed")
	}
	signed := *loaded.Record.SignedOutput
	if err := RestoreFromBundle(ctx, runner, RestoreFromBundleInput{FreshWorkDir: workDir, SourceRemotePath: CanonicalGitHubRemote, SourceSHA: signed.SourceSHA, BundlePath: bundlePath, ExpectedBundle: signed.Bundle, ExpectedReleaseSHA: signed.ReleaseSHA, ExpectedTreeSHA: signed.TreeSHA, ExpectedTags: signed.Tags}); err != nil {
		return result, err
	}
	verified, err := VerifyPublication(ctx, runner, PublicationVerificationInput{Loaded: loaded, Attestation: material.Attestation, TrustedPublicKey: mustSSHWire(contentSigner), Verifier: contentSigner, BundlePath: bundlePath, WorkDir: workDir})
	if err != nil {
		return result, err
	}
	return operation(ctx, runner, verified, store, loaded)
}

// PublishProductionBranches verifies durable recovery material and publishes
// only the exact resolved branch intents.
func PublishProductionBranches(ctx context.Context, input ProductionPublicationInput) (ProductionPublicationResult, error) {
	return withVerifiedProductionPublication(ctx, input, func(ctx context.Context, runner CommandRunner, verified VerifiedPublication, store *GitStateStore, loaded LoadResult) (ProductionPublicationResult, error) {
		return publishBranchesWithDurableProgress(ctx, runner, verified, store, loaded, CanonicalGitHubRemote)
	})
}

type reservationStatePersister interface {
	PersistReservation(context.Context, ReservationDecision, string) (string, error)
}

func publishBranchesWithDurableProgress(ctx context.Context, runner CommandRunner, verified VerifiedPublication, store reservationStatePersister, loaded LoadResult, remotePath string) (ProductionPublicationResult, error) {
	record, head := loaded.Record, loaded.RemoteHead
	if record.Phase != PhaseSigned || validateBranchIntents(record) != nil {
		return ProductionPublicationResult{}, newReleaseError(ErrorClassStateConflict, "signed_branch_intents_required")
	}
	result := PublicationResult{Record: record}
	for _, intent := range record.SignedOutput.PublicationIntents {
		state, err := classifyBranchRef(ctx, runner, verified.workDir, remotePath, intent)
		if err != nil {
			return ProductionPublicationResult{}, err
		}
		disposition := "reconciled"
		if state != RefDesired {
			if state == RefConflict || (intent.ExpectedOldSHA == "" && state != RefAbsent) || (intent.ExpectedOldSHA != "" && state != RefExpectedOld) {
				return ProductionPublicationResult{}, newReleaseError(ErrorClassStateConflict, "branch_ref_conflict")
			}
			pushErr := pushBranch(ctx, runner, verified.workDir, remotePath, intent)
			post, readErr := classifyBranchRef(ctx, runner, verified.workDir, remotePath, intent)
			if readErr != nil {
				return ProductionPublicationResult{}, readErr
			}
			if post != RefDesired {
				if pushErr != nil {
					return ProductionPublicationResult{}, wrapReleaseError(ErrorClassPublicationPartial, "branch_push_unconfirmed", pushErr)
				}
				return ProductionPublicationResult{}, newReleaseError(ErrorClassPublicationPartial, "branch_push_unconfirmed")
			}
			disposition = "published"
			result.PublishedRefs = append(result.PublishedRefs, intent.Ref)
		} else {
			result.ReconciledRefs = append(result.ReconciledRefs, intent.Ref)
		}
		if !hasRefEvent(record.Events, EventBranchPublished, intent.Ref, intent.DesiredSHA) {
			if err := appendRefEvent(&record, EventBranchPublished, intent.Ref, intent.DesiredSHA, disposition); err != nil {
				return ProductionPublicationResult{}, err
			}
			newHead, err := store.PersistReservation(ctx, ReservationDecision{Record: record}, head)
			if err != nil {
				return ProductionPublicationResult{}, err
			}
			head = newHead
		}
	}
	if err := advancePublicationPhase(&record, PhaseBranchesPublished); err != nil {
		return ProductionPublicationResult{}, err
	}
	newHead, err := store.PersistReservation(ctx, ReservationDecision{Record: record}, head)
	if err != nil {
		return ProductionPublicationResult{}, err
	}
	head = newHead
	result.Record = record
	return ProductionPublicationResult{Record: record, StateHead: head, Publication: result}, nil
}
