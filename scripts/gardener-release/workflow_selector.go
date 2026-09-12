// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
)

// OperationSelector is the read-only authorization result for one workflow run.
// StateHead binds every resume decision to one verified state-branch snapshot.
type OperationSelector struct {
	RecordFound      bool             `json:"record_found"`
	StateHead        string           `json:"state_head"`
	Phase            OperationPhase   `json:"phase"`
	NextJob          OrchestrationJob `json:"next_job"`
	Command          string           `json:"command"`
	FeedbackCategory string           `json:"feedback_category"`
	PolicyRevision   string           `json:"policy_revision"`
	RunReserve       bool             `json:"run_reserve"`
	RunGenerate      bool             `json:"run_generate"`
	RunSign          bool             `json:"run_sign"`
	RunBranches      bool             `json:"run_branches"`
	RunTests         bool             `json:"run_tests"`
	RunTags          bool             `json:"run_tags"`
	RunPreparePR     bool             `json:"run_prepare_pr"`
	RunImages        bool             `json:"run_images"`
	RunOutcome       bool             `json:"run_outcome"`
	Record           Record           `json:"record"`
}

// LoadProductionOperationSelector verifies the state branch without a private
// key or write token and selects the first unfinished phase.
func LoadProductionOperationSelector(ctx context.Context, request ValidatedRequest, policy Policy) (selector OperationSelector, err error) {
	verifier, err := newSSHPublicVerifier(policy.Signing)
	if err != nil {
		return selector, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, verifier.Close()) }()
	dir, err := osMkdirPrivateTemp("gardener-release-selector-")
	if err != nil {
		return selector, err
	}
	defer func() { err = ErrProtectedKeyCleanup(err, removePrivateTemp(dir)) }()
	store := NewGitStateStore(ExecRunner{}, CanonicalGitHubRemote, StateBranch, dir, nil, verifier, verifier.publicWire, CommitIdentity{}, RealClock{})
	if err := store.InitWorkingRepository(ctx); err != nil {
		return selector, err
	}
	loaded, err := store.LoadState(ctx, request.RequestKey)
	if err != nil {
		return selector, err
	}
	if !ValidGitObjectID(loaded.RemoteHead) {
		return selector, newReleaseError(ErrorClassStateConflict, "state_selector_head_invalid")
	}
	if err := verifySSHCommitWithPublicPolicy(ctx, ExecRunner{}, dir, loaded.RemoteHead, policy.Signing, verifier.allowedSigners); err != nil {
		return selector, err
	}
	return SelectOperation(request, loaded)
}

// SelectOperation derives a resume decision from one verified state snapshot.
func SelectOperation(request ValidatedRequest, loaded LoadResult) (OperationSelector, error) {
	selector := OperationSelector{
		RecordFound:      loaded.Found,
		StateHead:        loaded.RemoteHead,
		Command:          request.Command,
		PolicyRevision:   request.PolicyRevision,
		FeedbackCategory: "pending",
	}
	if !ValidGitObjectID(loaded.RemoteHead) {
		return OperationSelector{}, newReleaseError(ErrorClassStateConflict, "state_selector_head_invalid")
	}
	if !loaded.Found {
		selector.NextJob = JobReserveOperation
		setSelectorJobAuthorization(&selector)
		return selector, nil
	}
	if loaded.Record.Reservation.RequestKey != request.RequestKey || loaded.Record.Reservation.RequestSHA256 != request.RequestSHA256 || loaded.Record.Reservation.PolicyRevision != request.PolicyRevision || loaded.Record.Reservation.Command != request.Command {
		return OperationSelector{}, newReleaseError(ErrorClassStateConflict, "state_selector_binding_mismatch")
	}
	selector.Record = loaded.Record
	selector.Phase = loaded.Record.Phase
	switch loaded.Record.Phase {
	case PhaseReserved:
		selector.NextJob = JobGenerateUnsigned
		selector.FeedbackCategory = string(FeedbackPending)
	case PhaseSigned:
		selector.NextJob = JobPublishBranches
		selector.FeedbackCategory = string(FeedbackPartialPublication)
	case PhaseBranchesPublished:
		selector.NextJob = JobWaitBranchTests
		selector.FeedbackCategory = string(FeedbackTesting)
	case PhaseTestsPassed:
		selector.NextJob = JobPublishTags
		selector.FeedbackCategory = string(FeedbackPartialPublication)
	case PhaseTagsPublished:
		selector.FeedbackCategory = string(FeedbackTagsPublished)
		switch request.Command {
		case "release:prepare":
			selector.NextJob = JobEnsurePreparePR
		case "release:release":
			selector.NextJob = JobObserveImages
		case "release:promote":
			selector.NextJob = JobRecordOutcome
		default:
			return OperationSelector{}, newReleaseError(ErrorClassStateConflict, "state_selector_command_invalid")
		}
	case PhaseComplete:
		selector.NextJob = JobFeedback
		selector.FeedbackCategory = "complete"
	default:
		return OperationSelector{}, newReleaseError(ErrorClassStateConflict, "state_selector_phase_invalid")
	}
	setSelectorJobAuthorization(&selector)
	return selector, nil
}

func setSelectorJobAuthorization(selector *OperationSelector) {
	rank := map[OrchestrationJob]int{
		JobReserveOperation: 1, JobGenerateUnsigned: 2, JobValidateSignStore: 3,
		JobPublishBranches: 4, JobWaitBranchTests: 5, JobPublishTags: 6,
		JobEnsurePreparePR: 7, JobObserveImages: 7, JobRecordOutcome: 8, JobFeedback: 9,
	}[selector.NextJob]
	selector.RunReserve = rank > 0 && rank <= 1
	selector.RunGenerate = rank > 0 && rank <= 2
	selector.RunSign = rank > 0 && rank <= 3
	selector.RunBranches = rank > 0 && rank <= 4
	selector.RunTests = rank > 0 && rank <= 5
	selector.RunTags = rank > 0 && rank <= 6
	selector.RunPreparePR = selector.Command == "release:prepare" && rank > 0 && rank <= 7
	selector.RunImages = selector.Command == "release:release" && rank > 0 && rank <= 7
	selector.RunOutcome = rank > 0 && rank <= 8
}

// ValidateOperationSelector proves that selector is exactly the deterministic
// decision for its embedded durable-state evidence.
func ValidateOperationSelector(request ValidatedRequest, selector OperationSelector) error {
	selected, err := SelectOperation(request, LoadResult{Found: selector.RecordFound, RemoteHead: selector.StateHead, Record: selector.Record})
	if err != nil {
		return err
	}
	got, err := canonicalJSON(selector)
	if err != nil {
		return err
	}
	want, err := canonicalJSON(selected)
	if err != nil || string(got) != string(want) {
		return NewCLIError("selector_artifact_mismatch")
	}
	return nil
}

// FeedbackRecordForUnreservedRequest constructs only the immutable comment
// binding needed for request-bound pending feedback when validation succeeded
// but no durable reservation exists. It is not valid operation state and must
// never be passed to a state writer.
func FeedbackRecordForUnreservedRequest(request DispatchRequest, validated ValidatedRequest) (Record, error) {
	if request.RequestKey == "" || request.RequestKey != validated.RequestKey || request.RequestSHA256 != validated.RequestSHA256 ||
		request.Command != validated.Command || request.Version != validated.Version || request.Context.RepositoryID != validated.RepositoryID ||
		request.Context.RepositoryFullName != validated.RepositoryFullName || request.Context.IssueNumber != validated.IssueNumber ||
		request.Context.OriginalCommentID != validated.OriginalCommentID || request.Context.AcknowledgementCommentID != validated.AcknowledgementCommentID ||
		request.Context.PolicyRevision != validated.PolicyRevision {
		return Record{}, NewCLIError("feedback_unreserved_binding_mismatch")
	}
	return Record{Reservation: Reservation{
		SchemaVersion:            StateSchemaVersion,
		RequestKey:               validated.RequestKey,
		RequestSHA256:            validated.RequestSHA256,
		RepositoryID:             validated.RepositoryID,
		RepositoryFullName:       validated.RepositoryFullName,
		IssueNumber:              validated.IssueNumber,
		OriginalCommentID:        validated.OriginalCommentID,
		AcknowledgementCommentID: validated.AcknowledgementCommentID,
		Command:                  validated.Command,
		RequestedVersion:         validated.Version,
		BodySnapshot:             request.Context.BodySnapshot,
		ValidatedActorID:         validated.ValidatedActorID,
		ValidatedActorLogin:      validated.ValidatedActorLogin,
		PolicyRevision:           validated.PolicyRevision,
		ReleaseLine:              validated.ReleaseLine,
	}}, nil
}
