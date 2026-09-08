// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fakeChecksAPI struct {
	runs          []WorkflowRun
	jobs          map[string][]WorkflowJob
	current       map[string]WorkflowRun
	refs          map[string]string
	workflow      []byte
	runsNext      bool
	jobsNext      bool
	err           error
	currentMutate func(WorkflowRun) WorkflowRun
}

func (f *fakeChecksAPI) ListWorkflowRunsPage(_ context.Context, _ string, page, _ int) ([]WorkflowRun, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	if page == 1 {
		return f.runs, f.runsNext, nil
	}
	if f.runsNext {
		return nil, true, nil
	}
	return nil, false, nil
}
func (f *fakeChecksAPI) ListWorkflowJobsPage(_ context.Context, id string, _ int, page, _ int) ([]WorkflowJob, bool, error) {
	if page == 1 {
		return f.jobs[id], f.jobsNext, nil
	}
	if f.jobsNext {
		return nil, true, nil
	}
	return nil, false, nil
}
func (f *fakeChecksAPI) GetWorkflowRun(_ context.Context, id string) (WorkflowRun, error) {
	run, ok := f.current[id]
	if !ok {
		return WorkflowRun{}, errors.New("missing run")
	}
	if f.currentMutate != nil {
		run = f.currentMutate(run)
	}
	return run, nil
}
func (f *fakeChecksAPI) ReadWorkflowFile(_ context.Context, _, _ string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]byte(nil), f.workflow...), nil
}
func (f *fakeChecksAPI) ReadBranchRef(_ context.Context, ref string) (string, bool, error) {
	sha, ok := f.refs[ref]
	return sha, ok, nil
}

func checksPolicy() TestPolicy {
	digest := sha256.Sum256([]byte("approved workflow"))
	return TestPolicy{WorkflowPath: MainBranchTestWorkflowPath, WorkflowSHA256: stringHex(digest[:]), Event: "push", RequiredJobs: []string{"release-tests-complete"}, DeadlineSeconds: 1800}
}

func checksRecord(command string) Record {
	source := strings.Repeat("1", 40)
	release := strings.Repeat("2", 40)
	reservation := baseReservation()
	reservation.Command = command
	reservation.ReleaseLine = "v2.9"
	reservation.ResolvedVersion = "v2.9.0"
	signed := &SignedOutput{SourceSHA: source, ReleaseSHA: release}
	if command == "release:prepare" {
		reservation.BranchIntents = []BranchIntent{{Ref: "refs/heads/release-v2.9.x", DesiredSHA: source}, {Ref: "refs/heads/dev-v2.10.x", DesiredSHA: release}}
	} else {
		reservation.BranchIntents = []BranchIntent{{Ref: "refs/heads/release-v2.9.x", ExpectedOldSHA: source, DesiredSHA: release}}
	}
	return Record{Reservation: reservation, Phase: PhaseBranchesPublished, SignedOutput: signed}
}

func successfulChecksAPI(record Record, policy TestPolicy) *fakeChecksAPI {
	targets, _ := requiredTestTargets(record)
	api := &fakeChecksAPI{jobs: map[string][]WorkflowJob{}, current: map[string]WorkflowRun{}, refs: map[string]string{}, workflow: []byte("approved workflow")}
	for i, target := range targets {
		id := string(rune('1' + i))
		run := WorkflowRun{ID: id, RepositoryFullName: RepositoryFullName, WorkflowPath: policy.WorkflowPath, Event: "push", HeadBranch: target.Branch, HeadSHA: target.SHA, Attempt: 1, Status: "completed", Conclusion: "success"}
		api.runs = append(api.runs, run)
		api.current[id] = run
		api.jobs[id] = []WorkflowJob{{ID: string(rune('7' + i)), Name: "release-tests-complete", Attempt: 1, Status: "completed", Conclusion: "success"}}
		api.refs["refs/heads/"+target.Branch] = target.SHA
	}
	return api
}

func TestTestGatePrepareAndPromoteTargets(t *testing.T) {
	for _, command := range []string{"release:prepare", "release:promote", "release:release"} {
		t.Run(command, func(t *testing.T) {
			record := checksRecord(command)
			policy := checksPolicy()
			api := successfulChecksAPI(record, policy)
			result, err := VerifyRequiredTests(context.Background(), api, policy, record)
			if err != nil {
				t.Fatal(err)
			}
			wantRuns := 1
			if command == "release:prepare" {
				wantRuns = 2
			}
			if result.Record.Phase != PhaseTestsPassed || len(api.current) != wantRuns || !lowerHexDigest(result.Evidence.EvidenceSHA256) || result.Evidence.ReleaseSHA != record.SignedOutput.ReleaseSHA {
				t.Fatalf("result=%#v runs=%d", result, len(api.current))
			}
			if VerifyEventChain(record.Reservation.RequestKey, result.Record.Events) != nil || result.Record.Events[0].Kind != EventTestsPassed {
				t.Fatalf("events=%#v", result.Record.Events)
			}
			if err := RevalidateRequiredTests(context.Background(), api, policy, result.Record, result.Evidence); err != nil {
				t.Fatalf("immediate revalidation: %v", err)
			}
		})
	}
}

func TestTestGateRejectsPrefixOnlyBranchIntent(t *testing.T) {
	record := checksRecord("release:prepare")
	record.Reservation.BranchIntents[0].Ref = "refs/heads/release-v2.99.x"
	policy := checksPolicy()
	api := successfulChecksAPI(record, policy)
	if _, err := VerifyRequiredTests(context.Background(), api, policy, record); ErrorCode(err) != "invalid_branch_intent" {
		t.Fatalf("test gate error=%q, want invalid_branch_intent", ErrorCode(err))
	}
	record.Phase = PhaseTagsPublished
	if _, err := EnsurePreparePR(context.Background(), &fakePreparePRAPI{}, record); ErrorCode(err) != "invalid_branch_intent" {
		t.Fatalf("prepare PR error=%q, want invalid_branch_intent", ErrorCode(err))
	}
}

func TestTestGateT01OldGreenAndWrongEventRejected(t *testing.T) {
	record := checksRecord("release:promote")
	policy := checksPolicy()
	for _, mutate := range []func(*WorkflowRun){
		func(run *WorkflowRun) { run.HeadSHA = strings.Repeat("3", 40) },
		func(run *WorkflowRun) { run.Event = "push"; run.HeadBranch = "v2.9.0" },
		func(run *WorkflowRun) { run.Event = "workflow_dispatch" },
	} {
		api := successfulChecksAPI(record, policy)
		mutate(&api.runs[0])
		if _, err := VerifyRequiredTests(context.Background(), api, policy, record); ErrorCode(err) != "required_test_run_missing" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	}
}

func TestTestGateT02WrongWorkflowIdentityRejected(t *testing.T) {
	record := checksRecord("release:promote")
	policy := checksPolicy()
	for _, mutate := range []func(*WorkflowRun){
		func(run *WorkflowRun) { run.WorkflowPath = ".github/workflows/other.yml" },
		func(run *WorkflowRun) { run.HeadSHA = strings.Repeat("b", 40) },
		func(run *WorkflowRun) { run.RepositoryFullName = "attacker/fork" },
	} {
		api := successfulChecksAPI(record, policy)
		mutate(&api.runs[0])
		if _, err := VerifyRequiredTests(context.Background(), api, policy, record); ErrorCode(err) != "required_test_run_missing" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	}
	api := successfulChecksAPI(record, policy)
	api.workflow = []byte("unapproved workflow")
	if _, err := VerifyRequiredTests(context.Background(), api, policy, record); ErrorCode(err) != "workflow_code_mismatch" {
		t.Fatalf("error=%q", ErrorCode(err))
	}
	policy.WorkflowID = "99"
	api = successfulChecksAPI(record, policy)
	api.runs[0].WorkflowID = "98"
	if _, err := VerifyRequiredTests(context.Background(), api, policy, record); ErrorCode(err) != "required_test_run_missing" {
		t.Fatalf("error=%q", ErrorCode(err))
	}
}

func TestTestGateT03RequiredJobFailuresProduceNoTagEvidenceOrPush(t *testing.T) {
	for _, conclusion := range []string{"missing", "skipped", "cancelled", "failure", "timed_out"} {
		t.Run(conclusion, func(t *testing.T) {
			record := checksRecord("release:promote")
			policy := checksPolicy()
			api := successfulChecksAPI(record, policy)
			if conclusion == "missing" {
				api.jobs["1"] = nil
			} else {
				api.jobs["1"][0].Conclusion = conclusion
			}
			result, err := VerifyRequiredTests(context.Background(), api, policy, record)
			if ErrorCode(err) != "required_job_not_successful" || result.Evidence != (VerifiedTestEvidence{}) {
				t.Fatalf("error=%q result=%#v", ErrorCode(err), result)
			}
			remote := t.TempDir()
			runner := &countPushRunner{runner: ExecRunner{}}
			_, _ = PublishTags(context.Background(), runner, VerifiedPublication{record: record, workDir: t.TempDir()}, remote, result.Evidence)
			if runner.pushes != 0 {
				t.Fatalf("tag pushes=%d", runner.pushes)
			}
		})
	}
}

func TestTestGateT04PaginationAndAPIFailureBlock(t *testing.T) {
	record := checksRecord("release:promote")
	policy := checksPolicy()
	api := successfulChecksAPI(record, policy)
	api.runsNext = true
	if _, err := VerifyRequiredTests(context.Background(), api, policy, record); ErrorCode(err) != "pagination_exhausted" {
		t.Fatalf("error=%q", ErrorCode(err))
	}
	api = successfulChecksAPI(record, policy)
	api.err = context.DeadlineExceeded
	if _, err := VerifyRequiredTests(context.Background(), api, policy, record); ErrorCode(err) != "test_runs_read_failed" {
		t.Fatalf("error=%q", ErrorCode(err))
	}
}

func TestTestGateT05CurrentAttemptRerunOrCancellationInvalidatesEvidence(t *testing.T) {
	record := checksRecord("release:promote")
	policy := checksPolicy()
	for _, mutate := range []func(WorkflowRun) WorkflowRun{
		func(run WorkflowRun) WorkflowRun {
			run.Attempt++
			run.Status = "in_progress"
			run.Conclusion = ""
			return run
		},
		func(run WorkflowRun) WorkflowRun { run.Conclusion = "cancelled"; return run },
	} {
		api := successfulChecksAPI(record, policy)
		initial, err := VerifyRequiredTests(context.Background(), api, policy, record)
		if err != nil {
			t.Fatal(err)
		}
		api.currentMutate = mutate
		if err := RevalidateRequiredTests(context.Background(), api, policy, initial.Record, initial.Evidence); ErrorCode(err) != "stale_test_evidence" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	}
}

func TestTestGateBranchDriftAfterWaitRejected(t *testing.T) {
	record := checksRecord("release:promote")
	policy := checksPolicy()
	api := successfulChecksAPI(record, policy)
	api.refs["refs/heads/release-v2.9.x"] = strings.Repeat("3", 40)
	if _, err := VerifyRequiredTests(context.Background(), api, policy, record); ErrorCode(err) != "tested_branch_moved" {
		t.Fatalf("error=%q", ErrorCode(err))
	}
}

func TestMainBranchWorkflowStableGateAndWorkspaceIsolation(t *testing.T) {
	mainRaw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "main-branch-tests.yml"))
	if err != nil {
		t.Fatal(err)
	}
	main := string(mainRaw)
	for _, required := range []string{"- release-v*", "- dev-v*", "release-tests-complete:", "if: ${{ always() }}", "- unit-integration-tests", "- warm-repo-cache", "- multios-unit-tests", `test "${UNIT_INTEGRATION_RESULT}" = success`, `test "${WARM_REPO_CACHE_RESULT}" = success`, `test "${MULTIOS_UNIT_RESULT}" = success`} {
		if !strings.Contains(main, required) {
			t.Fatalf("main workflow missing %q", required)
		}
	}
	unitRaw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "unit-integration-tests.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unitRaw), "  GOWORK: off") {
		t.Fatal("reusable tests do not force GOWORK=off")
	}
	contribRaw, err := os.ReadFile(filepath.Join("..", "ci_test_contrib.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contribRaw), "go mod tidy") {
		t.Fatal("required contrib test path no longer runs tidy")
	}
	digest := sha256.Sum256(mainRaw)
	policy := checksPolicy()
	policy.WorkflowSHA256 = stringHex(digest[:])
	if err := validateTestPolicy(policy); err != nil {
		t.Fatal(err)
	}
}

func stringHex(value []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(value)*2)
	for i, b := range value {
		out[i*2], out[i*2+1] = digits[b>>4], digits[b&15]
	}
	return string(out)
}

func TestTestGateUsesLocalReplacementWithoutPublishedVersion(t *testing.T) {
	root := t.TempDir()
	dep := filepath.Join(root, "dep")
	app := filepath.Join(root, "app")
	if err := os.MkdirAll(dep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dep, "go.mod"), []byte("module example.test/dep/v2\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dep, "dep.go"), []byte("package dep\nfunc Value() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	appMod := "module example.test/app\n\ngo 1.26\n\nrequire example.test/dep/v2 v2.9.0-dev\nreplace example.test/dep/v2 => ../dep\n"
	if err := os.WriteFile(filepath.Join(app, "go.mod"), []byte(appMod), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "app_test.go"), []byte("package app\nimport (\"testing\"; \"example.test/dep/v2\")\nfunc TestValue(t *testing.T) { if dep.Value()!=1 { t.Fatal() } }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, "cache")
	env := append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off", "GOMODCACHE="+cache)
	for _, args := range [][]string{{"mod", "tidy"}, {"test", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = app
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("local replacement go %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(app, "go.mod"), []byte(strings.ReplaceAll(appMod, "replace example.test/dep/v2 => ../dep\n", "")), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = app
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off", "GOMODCACHE="+filepath.Join(root, "empty-cache"))
	if err := cmd.Run(); err == nil {
		t.Fatal("missing replacement unexpectedly resolved unpublished dependency")
	}
}

type fakePreparePRAPI struct {
	prs        []PreparePullRequest
	files      map[string][]string
	created    int
	createLost bool
	pageAlways bool
	listErr    error
}

func (f *fakePreparePRAPI) ListPullRequestsPage(_ context.Context, page, _ int) ([]PreparePullRequest, bool, error) {
	if f.listErr != nil {
		return nil, false, f.listErr
	}
	if page == 1 {
		return f.prs, f.pageAlways, nil
	}
	return nil, f.pageAlways, nil
}
func (f *fakePreparePRAPI) ListPullRequestFilesPage(_ context.Context, number string, page, _ int) ([]string, bool, error) {
	if page == 1 {
		return f.files[number], false, nil
	}
	return nil, false, nil
}
func (f *fakePreparePRAPI) CreatePullRequest(_ context.Context, input CreatePreparePullRequest) error {
	f.created++
	number := "42"
	f.prs = append(f.prs, PreparePullRequest{Number: number, RepositoryFullName: RepositoryFullName, HeadRef: input.HeadRef, HeadSHA: strings.Repeat("2", 40), BaseRef: input.BaseRef, State: "open", Body: input.Body})
	if f.files == nil {
		f.files = map[string][]string{}
	}
	f.files[number] = []string{"internal/version/version.go"}
	if f.createLost {
		return context.DeadlineExceeded
	}
	return nil
}

func preparePRRecord() Record {
	record := checksRecord("release:prepare")
	record.Phase = PhaseTagsPublished
	record.SignedOutput.ChangedPaths = []string{"internal/version/version.go"}
	return record
}

func TestPreparePRCreateLostResponseReconcilesAndResumeDeduplicates(t *testing.T) {
	record := preparePRRecord()
	api := &fakePreparePRAPI{files: map[string][]string{}, createLost: true}
	result, err := EnsurePreparePR(context.Background(), api, record)
	if err != nil {
		t.Fatal(err)
	}
	if result.PRNumber != "42" || api.created != 1 || result.Reconciled {
		t.Fatalf("result=%#v created=%d", result, api.created)
	}
	second, err := EnsurePreparePR(context.Background(), api, result.Record)
	if err != nil {
		t.Fatal(err)
	}
	if api.created != 1 || !second.Reconciled || len(second.Record.Events) != len(result.Record.Events) {
		t.Fatalf("second=%#v created=%d", second, api.created)
	}
}

func TestPreparePRD01WrongBaseOrHeadConflictsWithoutDuplicate(t *testing.T) {
	for _, mutate := range []func(*PreparePullRequest){
		func(pr *PreparePullRequest) { pr.BaseRef = "release-v2.9.x" },
		func(pr *PreparePullRequest) { pr.HeadRef = "release-v2.9.x" },
		func(pr *PreparePullRequest) { pr.HeadSHA = strings.Repeat("3", 40) },
		func(pr *PreparePullRequest) { pr.RepositoryFullName = "attacker/fork" },
		func(pr *PreparePullRequest) { pr.State = "closed" },
		func(pr *PreparePullRequest) { pr.Body = "wrong marker" },
	} {
		record := preparePRRecord()
		pr := PreparePullRequest{Number: "8", RepositoryFullName: RepositoryFullName, HeadRef: "dev-v2.10.x", HeadSHA: record.SignedOutput.ReleaseSHA, BaseRef: "main", State: "open", Body: preparePRMarker(record.Reservation.RequestKey)}
		mutate(&pr)
		api := &fakePreparePRAPI{prs: []PreparePullRequest{pr}, files: map[string][]string{"8": record.SignedOutput.ChangedPaths}}
		if _, err := EnsurePreparePR(context.Background(), api, record); ErrorCode(err) != "prepare_pr_conflict" || api.created != 0 {
			t.Fatalf("error=%q created=%d", ErrorCode(err), api.created)
		}
	}
}

func TestPreparePRChangedPathsAndPaginationFailClosed(t *testing.T) {
	record := preparePRRecord()
	pr := PreparePullRequest{Number: "8", RepositoryFullName: RepositoryFullName, HeadRef: "dev-v2.10.x", HeadSHA: record.SignedOutput.ReleaseSHA, BaseRef: "main", State: "open", Body: preparePRMarker(record.Reservation.RequestKey)}
	api := &fakePreparePRAPI{prs: []PreparePullRequest{pr}, files: map[string][]string{"8": {"unrelated.go"}}}
	if _, err := EnsurePreparePR(context.Background(), api, record); ErrorCode(err) != "prepare_pr_changed_paths" {
		t.Fatalf("error=%q", ErrorCode(err))
	}
	api = &fakePreparePRAPI{pageAlways: true}
	if _, err := EnsurePreparePR(context.Background(), api, record); ErrorCode(err) != "pagination_exhausted" {
		t.Fatalf("error=%q", ErrorCode(err))
	}
	api = &fakePreparePRAPI{listErr: context.DeadlineExceeded}
	if _, err := EnsurePreparePR(context.Background(), api, record); ErrorCode(err) != "prepare_pr_read_failed" {
		t.Fatalf("error=%q", ErrorCode(err))
	}
}
