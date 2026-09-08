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
	"fmt"
	"sort"
	"strings"
	"time"
)

const MainBranchTestWorkflowPath = ".github/workflows/main-branch-tests.yml"

// TestPolicy fixes the Actions evidence accepted by the pre-tag gate. WorkflowID
// is optional until administrators record GitHub's numeric ID; path plus the
// target-revision content digest remain mandatory and immutable.
type TestPolicy struct {
	WorkflowID      string
	WorkflowPath    string
	WorkflowSHA256  string
	Event           string
	RequiredJobs    []string
	DeadlineSeconds int
}

// WorkflowRun and WorkflowJob contain only fields used for exact evidence
// matching. Implementations must populate RepositoryFullName from the API
// response, not caller input.
type WorkflowRun struct {
	ID                 string
	RepositoryFullName string
	WorkflowID         string
	WorkflowPath       string
	Event              string
	HeadBranch         string
	HeadSHA            string
	Attempt            int
	Status             string
	Conclusion         string
}

type WorkflowJob struct {
	ID         string
	Name       string
	Attempt    int
	Status     string
	Conclusion string
}

// ChecksAPI is a read-only, page-explicit seam. Callers cannot accidentally
// accept a first-page-only answer; every page is consumed until hasNext=false.
type ChecksAPI interface {
	ListWorkflowRunsPage(context.Context, string, int, int) ([]WorkflowRun, bool, error)
	ListWorkflowJobsPage(context.Context, string, int, int, int) ([]WorkflowJob, bool, error)
	GetWorkflowRun(context.Context, string) (WorkflowRun, error)
	ReadWorkflowFile(context.Context, string, string) ([]byte, error)
	ReadBranchRef(context.Context, string) (string, bool, error)
}

type testTarget struct {
	Branch string
	SHA    string
}

type verifiedRunEvidence struct {
	TargetBranch string   `json:"target_branch"`
	TargetSHA    string   `json:"target_sha"`
	RunID        string   `json:"run_id"`
	Attempt      int      `json:"attempt"`
	JobIDs       []string `json:"job_ids"`
}

type testGateEvidence struct {
	SchemaVersion string                `json:"schema_version"`
	Repository    string                `json:"repository"`
	WorkflowPath  string                `json:"workflow_path"`
	WorkflowID    string                `json:"workflow_id,omitempty"`
	WorkflowSHA   string                `json:"workflow_sha256"`
	Event         string                `json:"event"`
	ReleaseSHA    string                `json:"release_sha"`
	Runs          []verifiedRunEvidence `json:"runs"`
}

type TestGateResult struct {
	Record   Record
	Evidence VerifiedTestEvidence
}

// VerifyRequiredTests evaluates exact current-attempt Actions evidence, then
// re-reads both runs and branch refs before recording success. It never pushes
// refs or calls source-tree code.
func VerifyRequiredTests(ctx context.Context, api ChecksAPI, policy TestPolicy, record Record) (TestGateResult, error) {
	if api == nil || record.Phase != PhaseBranchesPublished || record.SignedOutput == nil || !validRecordRepositoryBinding(record.Reservation) {
		return TestGateResult{}, newReleaseError(ErrorClassTestGateFailed, "tests_gate_not_ready")
	}
	if err := validateTestPolicy(policy); err != nil {
		return TestGateResult{}, err
	}
	if err := validateBranchIntents(record); err != nil {
		return TestGateResult{}, err
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, time.Duration(policy.DeadlineSeconds)*time.Second)
	defer cancel()
	ctx = deadlineCtx
	targets, err := requiredTestTargets(record)
	if err != nil {
		return TestGateResult{}, err
	}
	evidence := testGateEvidence{
		SchemaVersion: "1", Repository: RepositoryFullName, WorkflowPath: policy.WorkflowPath,
		WorkflowID: policy.WorkflowID, WorkflowSHA: policy.WorkflowSHA256, Event: policy.Event,
		ReleaseSHA: record.SignedOutput.ReleaseSHA,
	}
	matchedRuns := make([]WorkflowRun, 0, len(targets))
	for _, target := range targets {
		run, jobs, err := verifyTarget(ctx, api, policy, target)
		if err != nil {
			return TestGateResult{}, err
		}
		jobIDs := make([]string, 0, len(jobs))
		for _, job := range jobs {
			jobIDs = append(jobIDs, job.ID)
		}
		sort.Strings(jobIDs)
		evidence.Runs = append(evidence.Runs, verifiedRunEvidence{TargetBranch: target.Branch, TargetSHA: target.SHA, RunID: run.ID, Attempt: run.Attempt, JobIDs: jobIDs})
		matchedRuns = append(matchedRuns, run)
	}
	// Re-read each run after collecting all jobs. A rerun, cancellation, or
	// replacement invalidates the earlier evidence.
	for _, prior := range matchedRuns {
		current, err := api.GetWorkflowRun(ctx, prior.ID)
		if err != nil {
			return TestGateResult{}, wrapReleaseError(ErrorClassEvidenceIncomplete, "test_run_recheck_failed", err)
		}
		if !sameSuccessfulRun(current, prior) {
			return TestGateResult{}, newReleaseError(ErrorClassTestGateFailed, "stale_test_evidence")
		}
	}
	for _, target := range targets {
		sha, found, err := api.ReadBranchRef(ctx, "refs/heads/"+target.Branch)
		if err != nil {
			return TestGateResult{}, wrapReleaseError(ErrorClassEvidenceIncomplete, "branch_recheck_failed", err)
		}
		if !found || sha != target.SHA {
			return TestGateResult{}, newReleaseError(ErrorClassStateConflict, "tested_branch_moved")
		}
	}
	canonical, err := canonicalJSON(evidence)
	if err != nil {
		return TestGateResult{}, wrapReleaseError(ErrorClassEvidenceIncomplete, "test_evidence_marshal_failed", err)
	}
	digest := sha256.Sum256(canonical)
	verified := VerifiedTestEvidence{SchemaVersion: "1", ReleaseSHA: record.SignedOutput.ReleaseSHA, EvidenceSHA256: hex.EncodeToString(digest[:])}
	updated := record
	eventBody, err := json.Marshal(struct {
		Evidence VerifiedTestEvidence `json:"evidence"`
		Detail   testGateEvidence     `json:"detail"`
	}{Evidence: verified, Detail: evidence})
	if err != nil {
		return TestGateResult{}, err
	}
	updated.Events, err = AppendEvent(updated.Events, updated.Reservation.RequestKey, EventTestsPassed, eventBody)
	if err != nil {
		return TestGateResult{}, err
	}
	if err := advancePublicationPhase(&updated, PhaseTestsPassed); err != nil {
		return TestGateResult{}, err
	}
	return TestGateResult{Record: updated, Evidence: verified}, nil
}

// RevalidateRequiredTests repeats the complete current-attempt and branch-ref
// evaluation immediately before tag publication. B12 must call this with the
// protected tests_passed record; a stored green event alone is never enough.
func RevalidateRequiredTests(ctx context.Context, api ChecksAPI, policy TestPolicy, record Record, stored VerifiedTestEvidence) error {
	if !phaseAtLeast(record.Phase, PhaseTestsPassed) || record.SignedOutput == nil || stored.ReleaseSHA != record.SignedOutput.ReleaseSHA || !hasStoredTestEvidence(record.Events, stored) {
		return newReleaseError(ErrorClassTestGateFailed, "stored_test_evidence_invalid")
	}
	probe := record
	probe.Phase = PhaseBranchesPublished
	result, err := VerifyRequiredTests(ctx, api, policy, probe)
	if err != nil {
		return err
	}
	if result.Evidence != stored {
		return newReleaseError(ErrorClassTestGateFailed, "stale_test_evidence")
	}
	return nil
}

func hasStoredTestEvidence(events []Event, stored VerifiedTestEvidence) bool {
	for _, event := range events {
		if event.Kind != EventTestsPassed {
			continue
		}
		var body struct {
			Evidence VerifiedTestEvidence `json:"evidence"`
		}
		if json.Unmarshal(event.Evidence, &body) == nil && body.Evidence == stored {
			return true
		}
	}
	return false
}

func validateTestPolicy(policy TestPolicy) error {
	if policy.WorkflowPath != MainBranchTestWorkflowPath || !lowerHexDigest(policy.WorkflowSHA256) || policy.Event != "push" || policy.DeadlineSeconds <= 0 || policy.DeadlineSeconds > MaxPollingDeadlineSeconds || len(policy.RequiredJobs) == 0 || len(policy.RequiredJobs) > 100 {
		return newReleaseError(ErrorClassContractMismatch, "invalid_test_policy")
	}
	if policy.WorkflowID != "" && !validID(policy.WorkflowID) {
		return newReleaseError(ErrorClassContractMismatch, "invalid_test_policy")
	}
	seen := map[string]bool{}
	for _, job := range policy.RequiredJobs {
		if job == "" || strings.TrimSpace(job) != job || seen[job] {
			return newReleaseError(ErrorClassContractMismatch, "invalid_test_policy")
		}
		seen[job] = true
	}
	return nil
}

func requiredTestTargets(record Record) ([]testTarget, error) {
	intents := record.Reservation.BranchIntents
	branch := func(ref string) (string, bool) {
		if !strings.HasPrefix(ref, "refs/heads/") {
			return "", false
		}
		return strings.TrimPrefix(ref, "refs/heads/"), true
	}
	switch record.Reservation.Command {
	case "release:prepare":
		if len(intents) != 2 || intents[0].DesiredSHA != record.SignedOutput.SourceSHA || intents[1].DesiredSHA != record.SignedOutput.ReleaseSHA {
			return nil, newReleaseError(ErrorClassStateConflict, "invalid_test_targets")
		}
		first, ok1 := branch(intents[0].Ref)
		second, ok2 := branch(intents[1].Ref)
		if !ok1 || !ok2 || !strings.HasPrefix(first, "release-v") || !strings.HasPrefix(second, "dev-v") {
			return nil, newReleaseError(ErrorClassStateConflict, "invalid_test_targets")
		}
		return []testTarget{{Branch: first, SHA: intents[0].DesiredSHA}, {Branch: second, SHA: intents[1].DesiredSHA}}, nil
	case "release:promote", "release:release":
		if len(intents) != 1 || intents[0].DesiredSHA != record.SignedOutput.ReleaseSHA {
			return nil, newReleaseError(ErrorClassStateConflict, "invalid_test_targets")
		}
		name, ok := branch(intents[0].Ref)
		if !ok || !strings.HasPrefix(name, "release-v") {
			return nil, newReleaseError(ErrorClassStateConflict, "invalid_test_targets")
		}
		return []testTarget{{Branch: name, SHA: intents[0].DesiredSHA}}, nil
	default:
		return nil, newReleaseError(ErrorClassStateConflict, "invalid_test_targets")
	}
}

func verifyTarget(ctx context.Context, api ChecksAPI, policy TestPolicy, target testTarget) (WorkflowRun, []WorkflowJob, error) {
	var matches []WorkflowRun
	for page := 1; ; page++ {
		if page > MaxGitHubPages {
			return WorkflowRun{}, nil, newReleaseError(ErrorClassEvidenceIncomplete, "pagination_exhausted")
		}
		runs, next, err := api.ListWorkflowRunsPage(ctx, policy.WorkflowPath, page, GitHubPageSize)
		if err != nil {
			return WorkflowRun{}, nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "test_runs_read_failed", err)
		}
		for _, run := range runs {
			if runMatches(run, policy, target) {
				matches = append(matches, run)
			}
		}
		if !next {
			break
		}
	}
	if len(matches) != 1 {
		code := "required_test_run_missing"
		if len(matches) > 1 {
			code = "ambiguous_test_runs"
		}
		return WorkflowRun{}, nil, newReleaseError(ErrorClassTestGateFailed, code)
	}
	run := matches[0]
	workflow, err := api.ReadWorkflowFile(ctx, policy.WorkflowPath, target.SHA)
	if err != nil {
		return WorkflowRun{}, nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "workflow_code_read_failed", err)
	}
	workflowDigest := sha256.Sum256(workflow)
	if hex.EncodeToString(workflowDigest[:]) != policy.WorkflowSHA256 {
		return WorkflowRun{}, nil, newReleaseError(ErrorClassTestGateFailed, "workflow_code_mismatch")
	}
	if run.Status != "completed" || run.Conclusion != "success" {
		return WorkflowRun{}, nil, newReleaseError(ErrorClassTestGateFailed, "required_test_run_not_successful")
	}
	var jobs []WorkflowJob
	for page := 1; ; page++ {
		if page > MaxGitHubPages {
			return WorkflowRun{}, nil, newReleaseError(ErrorClassEvidenceIncomplete, "pagination_exhausted")
		}
		items, next, err := api.ListWorkflowJobsPage(ctx, run.ID, run.Attempt, page, GitHubPageSize)
		if err != nil {
			return WorkflowRun{}, nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "test_jobs_read_failed", err)
		}
		jobs = append(jobs, items...)
		if !next {
			break
		}
	}
	byName := make(map[string][]WorkflowJob)
	for _, job := range jobs {
		byName[job.Name] = append(byName[job.Name], job)
	}
	selected := make([]WorkflowJob, 0, len(policy.RequiredJobs))
	for _, name := range policy.RequiredJobs {
		matches := byName[name]
		if len(matches) != 1 || matches[0].Attempt != run.Attempt || matches[0].Status != "completed" || matches[0].Conclusion != "success" || !validID(matches[0].ID) {
			return WorkflowRun{}, nil, newReleaseError(ErrorClassTestGateFailed, "required_job_not_successful")
		}
		selected = append(selected, matches[0])
	}
	return run, selected, nil
}

func runMatches(run WorkflowRun, policy TestPolicy, target testTarget) bool {
	return validID(run.ID) && run.RepositoryFullName == RepositoryFullName &&
		(policy.WorkflowID == "" || run.WorkflowID == policy.WorkflowID) && run.WorkflowPath == policy.WorkflowPath &&
		run.Event == policy.Event && run.HeadBranch == target.Branch &&
		run.HeadSHA == target.SHA && run.Attempt > 0
}

func sameSuccessfulRun(current, prior WorkflowRun) bool {
	return current.ID == prior.ID && current.Attempt == prior.Attempt && current.Status == "completed" && current.Conclusion == "success" &&
		current.RepositoryFullName == prior.RepositoryFullName && current.WorkflowID == prior.WorkflowID &&
		current.WorkflowPath == prior.WorkflowPath && current.Event == prior.Event &&
		current.HeadBranch == prior.HeadBranch && current.HeadSHA == prior.HeadSHA
}

// PreparePullRequest is the bounded API representation used by PR reconciliation.
type PreparePullRequest struct {
	Number             string
	RepositoryFullName string
	HeadRef            string
	HeadSHA            string
	BaseRef            string
	State              string
	Body               string
}

type CreatePreparePullRequest struct {
	Title   string
	HeadRef string
	BaseRef string
	Body    string
}

type PreparePRAPI interface {
	ListPullRequestsPage(context.Context, int, int) ([]PreparePullRequest, bool, error)
	ListPullRequestFilesPage(context.Context, string, int, int) ([]string, bool, error)
	CreatePullRequest(context.Context, CreatePreparePullRequest) error
}

type PreparePRResult struct {
	Record     Record
	PRNumber   string
	Reconciled bool
}

// EnsurePreparePR creates or reconciles the prepare-only development PR after
// tags. It never merges and has no Git/push boundary.
func EnsurePreparePR(ctx context.Context, api PreparePRAPI, record Record) (PreparePRResult, error) {
	if api == nil || !validRecordRepositoryBinding(record.Reservation) || record.Reservation.Command != "release:prepare" || !phaseAtLeast(record.Phase, PhaseTagsPublished) || record.SignedOutput == nil || len(record.Reservation.BranchIntents) != 2 {
		return PreparePRResult{}, newReleaseError(ErrorClassStateConflict, "prepare_pr_not_applicable")
	}
	if err := validateBranchIntents(record); err != nil {
		return PreparePRResult{}, err
	}
	dev := record.Reservation.BranchIntents[1]
	if !strings.HasPrefix(dev.Ref, "refs/heads/dev-v") || dev.DesiredSHA != record.SignedOutput.ReleaseSHA {
		return PreparePRResult{}, newReleaseError(ErrorClassStateConflict, "invalid_prepare_pr_target")
	}
	head := strings.TrimPrefix(dev.Ref, "refs/heads/")
	marker := preparePRMarker(record.Reservation.RequestKey)
	find := func() (*PreparePullRequest, error) {
		prs, err := listAllPreparePRs(ctx, api)
		if err != nil {
			return nil, err
		}
		var exact *PreparePullRequest
		for i := range prs {
			pr := &prs[i]
			candidate := strings.Contains(pr.Body, marker) || pr.HeadRef == head
			if !candidate {
				continue
			}
			if pr.RepositoryFullName != RepositoryFullName || pr.HeadRef != head || pr.HeadSHA != dev.DesiredSHA || pr.BaseRef != "main" || pr.State != "open" || !strings.Contains(pr.Body, marker) {
				return nil, newReleaseError(ErrorClassStateConflict, "prepare_pr_conflict")
			}
			files, err := listAllPreparePRFiles(ctx, api, pr.Number)
			if err != nil {
				return nil, err
			}
			if !equalPathSets(files, record.SignedOutput.ChangedPaths) {
				return nil, newReleaseError(ErrorClassStateConflict, "prepare_pr_changed_paths")
			}
			if exact != nil {
				return nil, newReleaseError(ErrorClassStateConflict, "prepare_pr_ambiguous")
			}
			exact = pr
		}
		return exact, nil
	}
	pr, err := find()
	if err != nil {
		return PreparePRResult{}, err
	}
	reconciled := true
	if pr == nil {
		reconciled = false
		createErr := api.CreatePullRequest(ctx, CreatePreparePullRequest{
			Title: fmt.Sprintf("chore: merge %s into main", head), HeadRef: head, BaseRef: "main",
			Body: marker + "\n\nAutomated development-line update after verified release preparation.",
		})
		pr, err = find()
		if err != nil {
			return PreparePRResult{}, err
		}
		if pr == nil {
			if createErr != nil {
				return PreparePRResult{}, wrapReleaseError(ErrorClassPublicationPartial, "prepare_pr_create_unconfirmed", createErr)
			}
			return PreparePRResult{}, newReleaseError(ErrorClassPublicationPartial, "prepare_pr_create_unconfirmed")
		}
	}
	updated := record
	if !hasPreparePREvent(updated.Events, pr.Number) {
		body, _ := json.Marshal(struct {
			Number string `json:"number"`
			Head   string `json:"head"`
			SHA    string `json:"sha"`
			Base   string `json:"base"`
		}{Number: pr.Number, Head: head, SHA: dev.DesiredSHA, Base: "main"})
		updated.Events, err = AppendEvent(updated.Events, updated.Reservation.RequestKey, EventPreparePRRecorded, body)
		if err != nil {
			return PreparePRResult{}, err
		}
	}
	return PreparePRResult{Record: updated, PRNumber: pr.Number, Reconciled: reconciled}, nil
}

func validRecordRepositoryBinding(reservation Reservation) bool {
	parts := strings.Split(reservation.RequestKey, ":")
	return reservation.RepositoryFullName == RepositoryFullName && len(parts) == 2 && validID(parts[0]) && validID(parts[1]) && parts[0] == reservation.RepositoryID && parts[1] == reservation.OriginalCommentID
}

func preparePRMarker(requestKey string) string {
	return "<!-- gardener:release:prepare-pr:v1:" + requestKey + " -->"
}

func listAllPreparePRs(ctx context.Context, api PreparePRAPI) ([]PreparePullRequest, error) {
	var all []PreparePullRequest
	for page := 1; ; page++ {
		if page > MaxGitHubPages {
			return nil, newReleaseError(ErrorClassEvidenceIncomplete, "pagination_exhausted")
		}
		items, next, err := api.ListPullRequestsPage(ctx, page, GitHubPageSize)
		if err != nil {
			return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "prepare_pr_read_failed", err)
		}
		all = append(all, items...)
		if !next {
			return all, nil
		}
	}
}

func listAllPreparePRFiles(ctx context.Context, api PreparePRAPI, number string) ([]string, error) {
	if !validID(number) {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "invalid_prepare_pr")
	}
	var all []string
	for page := 1; ; page++ {
		if page > MaxGitHubPages {
			return nil, newReleaseError(ErrorClassEvidenceIncomplete, "pagination_exhausted")
		}
		items, next, err := api.ListPullRequestFilesPage(ctx, number, page, GitHubPageSize)
		if err != nil {
			return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "prepare_pr_files_read_failed", err)
		}
		all = append(all, items...)
		if !next {
			return all, nil
		}
	}
}

func equalPathSets(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	a := append([]string(nil), actual...)
	b := append([]string(nil), expected...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] || i > 0 && a[i] == a[i-1] {
			return false
		}
	}
	return true
}

func hasPreparePREvent(events []Event, number string) bool {
	for _, event := range events {
		if event.Kind != EventPreparePRRecorded {
			continue
		}
		var body struct {
			Number string `json:"number"`
		}
		if json.Unmarshal(event.Evidence, &body) == nil && body.Number == number {
			return true
		}
	}
	return false
}
