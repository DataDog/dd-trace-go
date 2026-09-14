// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "testing"

func hasOrchestrationJob(jobs []OrchestrationJob, want OrchestrationJob) bool {
	for _, job := range jobs {
		if job == want {
			return true
		}
	}
	return false
}

func assertNoOrchestrationJobs(t *testing.T, got []OrchestrationJob, forbidden ...OrchestrationJob) {
	t.Helper()
	for _, job := range forbidden {
		if hasOrchestrationJob(got, job) {
			t.Fatalf("jobs %v unexpectedly include %s", got, job)
		}
	}
}

func TestWorkflowW01FreshRequestRunsIntendedTrustDomains(t *testing.T) {
	decision, err := DecideWorkflowAttempt(WorkflowAttempt{RequestValid: true, BindingsValid: true, Command: "release:prepare"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []OrchestrationJob{JobValidateRequest, JobReserveOperation, JobGenerateUnsigned, JobValidateSignStore, JobPublishBranches, JobWaitBranchTests, JobPublishTags, JobEnsurePreparePR, JobRecordOutcome, JobFeedback} {
		if !hasOrchestrationJob(decision.Jobs, want) {
			t.Fatalf("jobs %v missing %s", decision.Jobs, want)
		}
	}
	if decision.Credentials[JobGenerateUnsigned] != CredentialsReadOnly || decision.Credentials[JobValidateSignStore] != CredentialsSigning || decision.Credentials[JobFeedback] != CredentialsFeedback {
		t.Fatalf("credential matrix = %#v", decision.Credentials)
	}
}

func TestWorkflowW02PartialResumeNeverRegeneratesOrResigns(t *testing.T) {
	decision, err := DecideWorkflowAttempt(WorkflowAttempt{RequestValid: true, BindingsValid: true, RecordFound: true, Phase: PhaseSigned, Command: "release:promote", DependencyProven: true})
	if err != nil {
		t.Fatal(err)
	}
	assertNoOrchestrationJobs(t, decision.Jobs, JobReserveOperation, JobGenerateUnsigned, JobValidateSignStore)
	if !hasOrchestrationJob(decision.Jobs, JobPublishBranches) || !hasOrchestrationJob(decision.Jobs, JobPublishTags) {
		t.Fatalf("jobs = %v", decision.Jobs)
	}
}

func TestWorkflowW03CompleteOperationIsVerifyAndFeedbackOnly(t *testing.T) {
	decision, err := DecideWorkflowAttempt(WorkflowAttempt{RequestValid: true, BindingsValid: true, RecordFound: true, Phase: PhaseComplete, Command: "release:release", DependencyProven: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Jobs) != 2 || decision.Jobs[0] != JobValidateRequest || decision.Jobs[1] != JobFeedback {
		t.Fatalf("jobs = %v", decision.Jobs)
	}
	assertNoOrchestrationJobs(t, decision.Jobs, JobGenerateUnsigned, JobValidateSignStore, JobPublishBranches, JobPublishTags, JobObserveImages)
}

func TestWorkflowW04BlockedOrConflictingStateAuthorizesNothing(t *testing.T) {
	for _, attempt := range []WorkflowAttempt{
		{RequestValid: true, BindingsValid: false},
		{RequestValid: true, BindingsValid: true, Blocked: true},
		{RequestValid: true, BindingsValid: true, RecordFound: true, Phase: PhaseSigned, Command: "release:release", DependencyProven: false},
	} {
		decision, err := DecideWorkflowAttempt(attempt)
		if err == nil {
			t.Fatalf("attempt %#v unexpectedly succeeded: %#v", attempt, decision)
		}
		assertNoOrchestrationJobs(t, decision.Jobs, JobReserveOperation, JobValidateSignStore, JobPublishBranches, JobPublishTags, JobFeedback)
	}
}

func TestWorkflowW05FeedbackOnlyCannotEnterPublication(t *testing.T) {
	decision, err := DecideWorkflowAttempt(WorkflowAttempt{BindingsValid: true, RecordFound: true, Phase: PhaseTagsPublished, FeedbackOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Jobs) != 1 || decision.Jobs[0] != JobFeedback {
		t.Fatalf("jobs = %v", decision.Jobs)
	}
}

func TestWorkflowW06InspectorAcknowledgedWithoutRecordRemainsInertUntilDispatch(t *testing.T) {
	decision, err := DecideWorkflowAttempt(WorkflowAttempt{AcknowledgedNoRecord: true})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.PendingNoRecord || len(decision.Jobs) != 0 {
		t.Fatalf("decision = %#v", decision)
	}
	dispatched, err := DecideWorkflowAttempt(WorkflowAttempt{RequestValid: true, BindingsValid: true, Command: "release:prepare"})
	if err != nil || !hasOrchestrationJob(dispatched.Jobs, JobReserveOperation) {
		t.Fatalf("exact approved redispatch did not reach reservation: %#v, %v", dispatched, err)
	}
}

func TestWorkflowW07ForgedDirectDispatchHasZeroPublication(t *testing.T) {
	decision, err := DecideWorkflowAttempt(WorkflowAttempt{BindingsValid: true, RequestValid: false})
	if err != nil {
		t.Fatal(err)
	}
	assertNoOrchestrationJobs(t, decision.Jobs, JobReserveOperation, JobValidateSignStore, JobPublishBranches, JobPublishTags, JobRecordOutcome)
	if !hasOrchestrationJob(decision.Jobs, JobValidateRequest) || hasOrchestrationJob(decision.Jobs, JobFeedback) {
		t.Fatalf("jobs = %v", decision.Jobs)
	}
}

func TestWorkflowW08CredentialMatrixAndEmptyOutputFailClosed(t *testing.T) {
	for job, domain := range workflowCredentialDomains {
		switch job {
		case JobGenerateUnsigned, JobValidateRequest, JobWaitBranchTests, JobObserveImages:
			if domain != CredentialsReadOnly {
				t.Fatalf("%s credential domain = %s", job, domain)
			}
		case JobFeedback:
			if domain != CredentialsFeedback {
				t.Fatalf("feedback domain = %s", domain)
			}
		}
	}
	decision, err := DecideWorkflowAttempt(WorkflowAttempt{BindingsValid: true, RequestValid: true, RecordFound: true, Phase: PhaseTestsPassed, Command: "release:release"})
	if err == nil {
		t.Fatalf("missing dependency proof authorized jobs: %#v", decision)
	}
}
