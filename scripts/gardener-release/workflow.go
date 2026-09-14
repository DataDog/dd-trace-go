// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

// OrchestrationJob is one logical B12 job. These names intentionally match the
// trusted workflow so the tested decision table remains source-discoverable.
type OrchestrationJob string

const (
	JobValidateRequest   OrchestrationJob = "validate_request"
	JobReserveOperation  OrchestrationJob = "reserve_operation"
	JobGenerateUnsigned  OrchestrationJob = "generate_unsigned"
	JobValidateSignStore OrchestrationJob = "validate_sign_store"
	JobPublishBranches   OrchestrationJob = "publish_branches"
	JobWaitBranchTests   OrchestrationJob = "wait_branch_tests"
	JobPublishTags       OrchestrationJob = "publish_tags"
	JobEnsurePreparePR   OrchestrationJob = "ensure_prepare_pr"
	JobObserveImages     OrchestrationJob = "observe_images"
	JobRecordOutcome     OrchestrationJob = "record_outcome"
	JobFeedback          OrchestrationJob = "feedback"
)

// CredentialDomain records the maximum credential class a job may receive.
type CredentialDomain string

const (
	CredentialsNone        CredentialDomain = "none"
	CredentialsReadOnly    CredentialDomain = "read_only"
	CredentialsPublication CredentialDomain = "protected_publication"
	CredentialsSigning     CredentialDomain = "protected_signing"
	CredentialsFeedback    CredentialDomain = "issues_write_only"
)

var workflowCredentialDomains = map[OrchestrationJob]CredentialDomain{
	JobValidateRequest:   CredentialsReadOnly,
	JobReserveOperation:  CredentialsPublication,
	JobGenerateUnsigned:  CredentialsReadOnly,
	JobValidateSignStore: CredentialsSigning,
	JobPublishBranches:   CredentialsPublication,
	JobWaitBranchTests:   CredentialsReadOnly,
	JobPublishTags:       CredentialsPublication,
	JobEnsurePreparePR:   CredentialsPublication,
	JobObserveImages:     CredentialsReadOnly,
	JobRecordOutcome:     CredentialsPublication,
	JobFeedback:          CredentialsFeedback,
}

// WorkflowAttempt is the complete trusted decision input for one attempt.
// Phase is accepted only when RecordFound is true. DependencyProven means the
// job outputs used to reach that phase were non-empty and verified against the
// signed durable record; a missing/skipped output can never set it.
type WorkflowAttempt struct {
	RequestValid         bool
	BindingsValid        bool
	RecordFound          bool
	Phase                OperationPhase
	Command              string
	FeedbackOnly         bool
	DependencyProven     bool
	AcknowledgedNoRecord bool
	Blocked              bool
}

// WorkflowDecision is a pure, deterministic authorization list. It grants no
// capability itself; each mutating function still requires its typed verified
// state capability.
type WorkflowDecision struct {
	Jobs            []OrchestrationJob                    `json:"jobs"`
	Credentials     map[OrchestrationJob]CredentialDomain `json:"credentials"`
	PendingNoRecord bool                                  `json:"pending_no_record"`
}

// DecideWorkflowAttempt implements B12's fresh/resume/feedback-only table.
func DecideWorkflowAttempt(attempt WorkflowAttempt) (WorkflowDecision, error) {
	decision := WorkflowDecision{Credentials: map[OrchestrationJob]CredentialDomain{}, PendingNoRecord: attempt.AcknowledgedNoRecord}
	add := func(job OrchestrationJob) {
		decision.Jobs = append(decision.Jobs, job)
		decision.Credentials[job] = workflowCredentialDomains[job]
	}
	if attempt.AcknowledgedNoRecord {
		if attempt.RecordFound {
			return WorkflowDecision{}, newReleaseError(ErrorClassStateConflict, "pending_record_conflict")
		}
		// An acknowledged request that never acquired the lock is operator
		// evidence only. It is never an implicit new reservation.
		return decision, nil
	}
	if !attempt.BindingsValid {
		return decision, newReleaseError(ErrorClassRequestRejected, "request_binding_invalid")
	}
	if attempt.Blocked {
		return decision, newReleaseError(ErrorClassStateConflict, "workflow_blocked")
	}
	if attempt.FeedbackOnly {
		if !attempt.RecordFound || !KnownPhase(attempt.Phase) {
			return WorkflowDecision{}, newReleaseError(ErrorClassStateConflict, "feedback_record_required")
		}
		add(JobFeedback)
		return decision, nil
	}
	add(JobValidateRequest)
	if !attempt.RequestValid {
		// Validation failure produces no trusted selector artifact. Gardener owns
		// pre-dispatch rejection feedback; this workflow authorizes no mutation.
		return decision, nil
	}
	if !attempt.RecordFound {
		add(JobReserveOperation)
		add(JobGenerateUnsigned)
		add(JobValidateSignStore)
		add(JobPublishBranches)
		add(JobWaitBranchTests)
		add(JobPublishTags)
		addApplicablePostTagJobs(&decision, add, attempt.Command)
		add(JobRecordOutcome)
		add(JobFeedback)
		return decision, nil
	}
	if !KnownPhase(attempt.Phase) || !attempt.DependencyProven {
		return WorkflowDecision{}, newReleaseError(ErrorClassStateConflict, "resume_evidence_required")
	}
	switch attempt.Phase {
	case PhaseReserved:
		add(JobGenerateUnsigned)
		add(JobValidateSignStore)
		add(JobPublishBranches)
		add(JobWaitBranchTests)
		add(JobPublishTags)
		addApplicablePostTagJobs(&decision, add, attempt.Command)
		add(JobRecordOutcome)
	case PhaseSigned:
		add(JobPublishBranches)
		add(JobWaitBranchTests)
		add(JobPublishTags)
		addApplicablePostTagJobs(&decision, add, attempt.Command)
		add(JobRecordOutcome)
	case PhaseBranchesPublished:
		add(JobWaitBranchTests)
		add(JobPublishTags)
		addApplicablePostTagJobs(&decision, add, attempt.Command)
		add(JobRecordOutcome)
	case PhaseTestsPassed:
		add(JobPublishTags)
		addApplicablePostTagJobs(&decision, add, attempt.Command)
		add(JobRecordOutcome)
	case PhaseTagsPublished:
		addApplicablePostTagJobs(&decision, add, attempt.Command)
		add(JobRecordOutcome)
	case PhaseComplete:
		// The selector verified the durable outcome. Complete resumes only
		// request-bound feedback and performs no protected state write.
	}
	add(JobFeedback)
	return decision, nil
}

func addApplicablePostTagJobs(_ *WorkflowDecision, add func(OrchestrationJob), command string) {
	switch command {
	case "release:prepare":
		add(JobEnsurePreparePR)
	case "release:release":
		add(JobObserveImages)
	}
}

// CredentialDomainForJob returns the fixed maximum credential class.
func CredentialDomainForJob(job OrchestrationJob) (CredentialDomain, bool) {
	domain, ok := workflowCredentialDomains[job]
	return domain, ok
}
