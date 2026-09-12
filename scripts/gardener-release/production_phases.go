// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"reflect"
)

type protectedStateOperation func(context.Context, CommandRunner, *GitStateStore, LoadResult) error

// withProtectedProductionState owns every publication credential, signing key,
// askpass helper and temporary directory used by a state-writing phase.
func withProtectedProductionState(ctx context.Context, input ProductionPublicationInput, operation protectedStateOperation) (loaded LoadResult, err error) {
	if operation == nil || !ValidGitObjectID(input.ExpectedStateHead) || !validRecordRepositoryBinding(input.Record.Reservation) {
		return loaded, newReleaseError(ErrorClassStateConflict, "protected_state_input_invalid")
	}
	credentials, err := LoadProtectedGitHubGitCredentials()
	if err != nil {
		return loaded, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, credentials.Close()) }()
	gitSigning, err := LoadProtectedSSHSigningKey(input.Policy.Signing)
	if err != nil {
		return loaded, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, gitSigning.Close()) }()
	contentSigner, err := NewSSHContentSigner(gitSigning)
	if err != nil {
		return loaded, err
	}
	runner := ProtectedGitRunner{Runner: ExecRunner{}, Credentials: credentials}
	stateDir, err := osMkdirPrivateTemp("gardener-release-state-")
	if err != nil {
		return loaded, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, removePrivateTemp(stateDir)) }()
	store, err := NewProtectedGitStateStore(runner, stateDir, contentSigner, contentSigner, mustSSHWire(contentSigner), CommitIdentity{Name: "gardener-release", Email: input.Policy.Signing.Principal}, RealClock{}, gitSigning)
	if err != nil {
		return loaded, err
	}
	if err := store.InitWorkingRepository(ctx); err != nil {
		return loaded, err
	}
	loaded, err = store.LoadState(ctx, input.Record.Reservation.RequestKey)
	if err != nil {
		return loaded, err
	}
	if !loaded.Found || loaded.RemoteHead != input.ExpectedStateHead || !recordsEqual(loaded.Record, input.Record) {
		return loaded, newReleaseError(ErrorClassStateConflict, "state_changed_before_phase")
	}
	if err := operation(ctx, runner, store, loaded); err != nil {
		return LoadResult{}, err
	}
	return loaded, nil
}

func sameVerifiedTestEvidence(left, right VerifiedTestEvidence) bool {
	return reflect.DeepEqual(left, right)
}

type ProductionTestGateResult struct {
	Record    Record               `json:"record"`
	StateHead string               `json:"state_head"`
	Evidence  VerifiedTestEvidence `json:"test_evidence"`
}

func WaitProductionBranchTests(ctx context.Context, input ProductionPublicationInput) (ProductionTestGateResult, error) {
	readToken := os.Getenv("GARDENER_RELEASE_READ_TOKEN")
	if readToken == "" {
		return ProductionTestGateResult{}, newReleaseError(ErrorClassEvidenceIncomplete, "read_token_unavailable")
	}
	loaded, err := LoadProductionStateReadOnly(ctx, input.Record, input.ExpectedStateHead, input.Policy)
	if err != nil {
		return ProductionTestGateResult{}, err
	}
	gate, err := VerifyRequiredTests(ctx, NewProductionGitHubClient(readToken), input.Policy.Test, loaded)
	if err != nil {
		return ProductionTestGateResult{}, err
	}
	// The read-only job transports verified evidence. The protected tag job
	// persists the tests-passed transition before it mutates any tag ref.
	return ProductionTestGateResult{Record: loaded, StateHead: input.ExpectedStateHead, Evidence: gate.Evidence}, nil
}

func PublishProductionTags(ctx context.Context, input ProductionPublicationInput, stored VerifiedTestEvidence) (ProductionPublicationResult, error) {
	readToken := os.Getenv("GARDENER_RELEASE_READ_TOKEN")
	if readToken == "" {
		return ProductionPublicationResult{}, newReleaseError(ErrorClassEvidenceIncomplete, "read_token_unavailable")
	}
	api := NewProductionGitHubClient(readToken)
	if input.Record.Reservation.Command == "release:release" {
		if err := verifyTrustedImageWorkflows(ctx, api, input.Policy.Image, input.Record.SignedOutput.ReleaseSHA); err != nil {
			return ProductionPublicationResult{}, err
		}
	}
	return withVerifiedProductionPublication(ctx, input, func(ctx context.Context, runner CommandRunner, verified VerifiedPublication, store *GitStateStore, loaded LoadResult) (ProductionPublicationResult, error) {
		record, head := loaded.Record, loaded.RemoteHead
		if loaded.Record.Phase == PhaseBranchesPublished {
			gate, err := VerifyRequiredTests(ctx, api, input.Policy.Test, loaded.Record)
			if err != nil {
				return ProductionPublicationResult{}, err
			}
			if !sameVerifiedTestEvidence(gate.Evidence, stored) {
				return ProductionPublicationResult{}, newReleaseError(ErrorClassTestGateFailed, "test_evidence_changed")
			}
			// Persist the exact test evidence before the first tag mutation.
			head, err = store.PersistReservation(ctx, ReservationDecision{Record: gate.Record}, loaded.RemoteHead)
			if err != nil {
				return ProductionPublicationResult{}, err
			}
			record = gate.Record
		} else if loaded.Record.Phase != PhaseTestsPassed {
			return ProductionPublicationResult{}, newReleaseError(ErrorClassStateConflict, "tests_phase_required")
		}
		result := PublicationResult{Record: record}
		for _, tag := range record.SignedOutput.Tags {
			if err := RevalidateRequiredTests(ctx, api, input.Policy.Test, record, stored); err != nil {
				return ProductionPublicationResult{}, err
			}
			state, err := classifyTagRef(ctx, runner, verified.workDir, CanonicalGitHubRemote, tag)
			if err != nil {
				return ProductionPublicationResult{}, err
			}
			disposition := "reconciled"
			if state != RefDesired {
				if record.Phase != PhaseTestsPassed || state != RefAbsent {
					return ProductionPublicationResult{}, newReleaseError(ErrorClassStateConflict, "tag_ref_conflict")
				}
				pushErr := pushTag(ctx, runner, verified.workDir, CanonicalGitHubRemote, tag)
				post, readErr := classifyTagRef(ctx, runner, verified.workDir, CanonicalGitHubRemote, tag)
				if readErr != nil {
					return ProductionPublicationResult{}, readErr
				}
				if post != RefDesired {
					if pushErr != nil {
						return ProductionPublicationResult{}, wrapReleaseError(ErrorClassPublicationPartial, "tag_push_unconfirmed", pushErr)
					}
					return ProductionPublicationResult{}, newReleaseError(ErrorClassPublicationPartial, "tag_push_unconfirmed")
				}
				disposition = "published"
				result.PublishedRefs = append(result.PublishedRefs, tag.Ref)
			} else {
				result.ReconciledRefs = append(result.ReconciledRefs, tag.Ref)
			}
			if record.Phase == PhaseTestsPassed && !hasRefEvent(record.Events, EventTagPublished, tag.Ref, tag.TagObjectSHA) {
				if err := appendRefEvent(&record, EventTagPublished, tag.Ref, tag.TagObjectSHA, disposition); err != nil {
					return ProductionPublicationResult{}, err
				}
				newHead, err := store.PersistReservation(ctx, ReservationDecision{Record: record}, head)
				if err != nil {
					return ProductionPublicationResult{}, err
				}
				head = newHead
			}
		}
		if record.Phase == PhaseTestsPassed {
			if err := advancePublicationPhase(&record, PhaseTagsPublished); err != nil {
				return ProductionPublicationResult{}, err
			}
			newHead, err := store.PersistReservation(ctx, ReservationDecision{Record: record}, head)
			if err != nil {
				return ProductionPublicationResult{}, err
			}
			head = newHead
		}
		result.Record = record
		return ProductionPublicationResult{Record: record, StateHead: head, Publication: result}, nil
	})
}

func verifyTrustedImageWorkflows(ctx context.Context, api ChecksAPI, policy ImageObservationPolicy, releaseSHA string) error {
	for path, want := range map[string]string{ImageWorkflowPath: policy.WorkflowSHA256, ImageChildWorkflowPath: policy.ChildWorkflowSHA256} {
		if !lowerHexDigest(want) {
			return newReleaseError(ErrorClassContractMismatch, "image_workflow_policy_invalid")
		}
		raw, err := api.ReadWorkflowFile(ctx, path, releaseSHA)
		if err != nil {
			return wrapReleaseError(ErrorClassEvidenceIncomplete, "image_workflow_read_failed", err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != want {
			return newReleaseError(ErrorClassTestGateFailed, "image_workflow_code_mismatch")
		}
	}
	return nil
}

type ProductionPreparePRResult struct {
	Record    Record      `json:"record"`
	StateHead string      `json:"state_head"`
	PRNumber  string      `json:"pr_number"`
	Outcome   WorkOutcome `json:"outcome"`
}

func EnsureProductionPreparePR(ctx context.Context, input ProductionPublicationInput) (result ProductionPreparePRResult, err error) {
	token := os.Getenv(PublicationTokenEnvironment)
	if token == "" {
		return result, newReleaseError(ErrorClassEvidenceIncomplete, "publication_token_unavailable")
	}
	_, err = withProtectedProductionState(ctx, input, func(ctx context.Context, _ CommandRunner, store *GitStateStore, loaded LoadResult) error {
		pr, prErr := EnsurePreparePR(ctx, NewProductionGitHubClient(token), loaded.Record)
		if prErr != nil {
			return prErr
		}
		head := loaded.RemoteHead
		if !recordsEqual(pr.Record, loaded.Record) {
			head, prErr = store.PersistReservation(ctx, ReservationDecision{Record: pr.Record}, loaded.RemoteHead)
			if prErr != nil {
				return prErr
			}
		}
		result = ProductionPreparePRResult{Record: pr.Record, StateHead: head, PRNumber: pr.PRNumber, Outcome: WorkSucceeded}
		return nil
	})
	return result, err
}

type ProductionOutcomeResult struct {
	Record    Record           `json:"record"`
	StateHead string           `json:"state_head"`
	Outcome   OperationOutcome `json:"outcome"`
}

func RecordProductionOutcome(ctx context.Context, input ProductionPublicationInput, outcome OperationOutcome) (result ProductionOutcomeResult, err error) {
	_, err = withProtectedProductionState(ctx, input, func(ctx context.Context, _ CommandRunner, store *GitStateStore, loaded LoadResult) error {
		updated, updateErr := RecordOperationOutcome(loaded.Record, outcome)
		if updateErr != nil {
			return updateErr
		}
		head := loaded.RemoteHead
		if !recordsEqual(updated, loaded.Record) {
			head, updateErr = store.PersistReservation(ctx, ReservationDecision{Record: updated}, loaded.RemoteHead)
			if updateErr != nil {
				return updateErr
			}
		}
		result = ProductionOutcomeResult{Record: updated, StateHead: head, Outcome: outcome}
		return nil
	})
	return result, err
}
