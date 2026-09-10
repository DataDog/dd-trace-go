// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func releaseWorkflowText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "gardener-release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWorkflowYAMLInvokesEveryFunctionalPhaseCommand(t *testing.T) {
	text := releaseWorkflowText(t)
	commands := []string{"validate-request", "reserve-operation", "generate-unsigned", "validate-sign-store", "publish-branches", "wait-branch-tests", "publish-tags", "ensure-prepare-pr", "observe-images", "record-outcome", "publish-feedback"}
	for _, command := range commands {
		if strings.Count(text, "gardener-release\" "+command) == 0 {
			t.Errorf("workflow does not invoke %s", command)
		}
	}
	jobs := []string{"validate_request", "reserve_operation", "generate_unsigned", "validate_sign_store", "publish_branches", "wait_branch_tests", "publish_tags", "ensure_prepare_pr", "observe_images", "record_outcome", "feedback"}
	for _, job := range jobs {
		if !strings.Contains(text, "\n  "+job+":\n") {
			t.Errorf("workflow is missing job %s", job)
		}
	}
	if strings.Contains(text, "not-ready") || strings.Contains(text, "Gardener release automation is not ready") {
		t.Fatal("fail-closed skeleton remains")
	}
}

func TestWorkflowYAMLPinsActionsAndMediatesExpressions(t *testing.T) {
	text := releaseWorkflowText(t)
	uses := regexp.MustCompile(`(?m)^\s*uses:\s*[^#\s]+@([^\s#]+)`).FindAllStringSubmatch(text, -1)
	if len(uses) == 0 {
		t.Fatal("workflow has no actions")
	}
	fullSHA := regexp.MustCompile(`^[0-9a-f]{40}$`)
	for _, match := range uses {
		if !fullSHA.MatchString(match[1]) {
			t.Errorf("action revision is not immutable: %s", match[1])
		}
	}
	for _, forbidden := range []string{"secrets: inherit", "http.extraHeader", "x-access-token:", "--force ", "--mirror", "${{ inputs.context }}'", "github.event.inputs.context"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("workflow contains forbidden token %q", forbidden)
		}
	}
	if !strings.Contains(text, "persist-credentials: false") || !strings.Contains(text, "INPUT_CONTEXT: ${{ inputs.context }}") {
		t.Fatal("checkout or dispatch expression mediation is missing")
	}
}

func TestDockerImageWorkflowGatesPackageJobsOnBothWorkflowDigests(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "docker-images-release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, token := range []string{
		"GARDENER_RELEASE_DOCKER_IMAGES_WORKFLOW_SHA256",
		"GARDENER_RELEASE_DOCKER_BUILD_WORKFLOW_SHA256",
		"EXPECTED_PARENT_SHA256",
		"EXPECTED_CHILD_SHA256",
		".github/workflows/docker-images-release.yml | sha256sum --check --strict -",
		".github/workflows/docker-build-and-push.yml | sha256sum --check --strict -",
		"needs: [prepare-tag, verify-release-workflows]",
	} {
		if !strings.Contains(text, token) {
			t.Errorf("image workflow lacks trusted child gate token %q", token)
		}
	}
	if strings.Count(text, "needs: [prepare-tag, verify-release-workflows]") != 4 {
		t.Fatal("not every package-writing image job depends on the workflow digest gate")
	}
	if strings.Count(text, "if [ \"$EVENT_NAME\" = workflow_dispatch ]") != 1 {
		t.Fatal("manual package writes bypass both reviewed workflow digests")
	}
}

func TestWorkflowYAMLUsesFixedUnsignedBundleName(t *testing.T) {
	text := releaseWorkflowText(t)
	if strings.Count(text, UnsignedRepositoryBundleName) != 2 || strings.Contains(text, "/unsigned.bundle") {
		t.Fatalf("workflow must use fixed unsigned bundle basename %q", UnsignedRepositoryBundleName)
	}
}

func TestWorkflowYAMLCredentialDomainsW08(t *testing.T) {
	text := releaseWorkflowText(t)
	sections := map[string]string{}
	jobs := []string{"validate_request", "reserve_operation", "generate_unsigned", "validate_sign_store", "publish_branches", "wait_branch_tests", "publish_tags", "ensure_prepare_pr", "observe_images", "record_outcome", "feedback"}
	for index, job := range jobs {
		start := strings.Index(text, "\n  "+job+":\n")
		if start < 0 {
			t.Fatalf("missing job %s", job)
		}
		end := len(text)
		if index+1 < len(jobs) {
			if next := strings.Index(text[start+1:], "\n  "+jobs[index+1]+":\n"); next >= 0 {
				end = start + 1 + next
			}
		}
		sections[job] = text[start:end]
	}
	for _, job := range []string{"reserve_operation", "validate_sign_store", "publish_branches", "publish_tags", "ensure_prepare_pr", "record_outcome"} {
		if !strings.Contains(sections[job], "environment: gardener-release") || !strings.Contains(sections[job], "id-token: write") {
			t.Errorf("protected job %s lacks its environment/token boundary", job)
		}
		if !strings.Contains(sections[job], "Remove protected temporary material") || !strings.Contains(sections[job], "if: ${{ always() }}") {
			t.Errorf("protected job %s lacks always-run cleanup", job)
		}
	}
	for _, job := range []string{"validate_request", "generate_unsigned", "wait_branch_tests", "observe_images", "feedback"} {
		if strings.Contains(sections[job], "environment: gardener-release") || strings.Contains(sections[job], "GARDENER_RELEASE_SSH_SIGNING_KEY") {
			t.Errorf("unprivileged job %s receives signing environment", job)
		}
	}
	for _, job := range []string{"wait_branch_tests", "observe_images"} {
		if strings.Contains(sections[job], "GARDENER_RELEASE_GITHUB_TOKEN") || strings.Contains(sections[job], "dd-octo-sts-action") {
			t.Errorf("read-only job %s receives publication credentials", job)
		}
	}
	if strings.Contains(sections["feedback"], "GARDENER_RELEASE_GITHUB_TOKEN") || !strings.Contains(sections["feedback"], "GARDENER_RELEASE_ISSUES_TOKEN") {
		t.Fatal("feedback credential boundary is invalid")
	}
}

func workflowJobConditions(text string) map[string]string {
	conditions := map[string]string{}
	job := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.HasSuffix(line, ":") {
			job = strings.TrimSuffix(strings.TrimSpace(line), ":")
			continue
		}
		if job != "" && strings.HasPrefix(line, "    if: ${{ ") {
			conditions[job] = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(line), "if: ${{ "), " }}")
		}
	}
	return conditions
}

type workflowBoolParser struct {
	tokens []string
	index  int
}

func (p *workflowBoolParser) expression() (bool, error) {
	value, err := p.term()
	for err == nil && p.index < len(p.tokens) && p.tokens[p.index] == "||" {
		p.index++
		right, nextErr := p.term()
		value, err = value || right, nextErr
	}
	return value, err
}

func (p *workflowBoolParser) term() (bool, error) {
	value, err := p.factor()
	for err == nil && p.index < len(p.tokens) && p.tokens[p.index] == "&&" {
		p.index++
		right, nextErr := p.factor()
		value, err = value && right, nextErr
	}
	return value, err
}

func (p *workflowBoolParser) factor() (bool, error) {
	if p.index >= len(p.tokens) {
		return false, fmt.Errorf("missing boolean factor")
	}
	token := p.tokens[p.index]
	p.index++
	switch token {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "(":
		value, err := p.expression()
		if err != nil || p.index >= len(p.tokens) || p.tokens[p.index] != ")" {
			return false, fmt.Errorf("unclosed boolean group")
		}
		p.index++
		return value, nil
	default:
		return false, fmt.Errorf("unexpected boolean token %q", token)
	}
}

func evaluateWorkflowCondition(condition string, values map[string]string) (bool, error) {
	condition = strings.ReplaceAll(condition, "always()", "true")
	comparison := regexp.MustCompile(`([A-Za-z0-9_.]+) == '([^']*)'`)
	condition = comparison.ReplaceAllStringFunc(condition, func(match string) string {
		parts := comparison.FindStringSubmatch(match)
		return fmt.Sprintf("%t", values[parts[1]] == parts[2])
	})
	replacer := strings.NewReplacer("(", " ( ", ")", " ) ", "&&", " && ", "||", " || ")
	parser := workflowBoolParser{tokens: strings.Fields(replacer.Replace(condition))}
	value, err := parser.expression()
	if err != nil {
		return false, err
	}
	if parser.index != len(parser.tokens) {
		return false, fmt.Errorf("unevaluated condition tokens %v", parser.tokens[parser.index:])
	}
	return value, nil
}

func workflowOutputFlags(next OrchestrationJob, command string) map[string]string {
	selector := OperationSelector{NextJob: next, Command: command}
	setSelectorJobAuthorization(&selector)
	values := map[string]string{"needs.validate_request.outputs.next_job": string(next), "needs.validate_request.outputs.command": command}
	for name, value := range map[string]bool{
		"reserve": selector.RunReserve, "generate": selector.RunGenerate, "sign": selector.RunSign,
		"branches": selector.RunBranches, "wait": selector.RunTests, "tags": selector.RunTags,
		"prepare": selector.RunPreparePR, "images": selector.RunImages, "outcome": selector.RunOutcome,
	} {
		values["needs.validate_request.outputs.run_"+name] = strconv.FormatBool(value)
	}
	return values
}

func TestWorkflowYAMLConditionsEvaluateW01ThroughW08(t *testing.T) {
	conditions := workflowJobConditions(releaseWorkflowText(t))
	jobOrder := []string{"validate_request", "reserve_operation", "generate_unsigned", "validate_sign_store", "publish_branches", "wait_branch_tests", "publish_tags", "ensure_prepare_pr", "observe_images", "record_outcome", "feedback"}
	cases := []struct {
		name               string
		next               OrchestrationJob
		command            string
		validationSucceeds bool
		failJob            string
		ref                string
		want               []string
	}{
		{"W01 fresh prepare", JobReserveOperation, "release:prepare", true, "", "refs/heads/main", []string{"validate_request", "reserve_operation", "generate_unsigned", "validate_sign_store", "publish_branches", "wait_branch_tests", "publish_tags", "ensure_prepare_pr", "record_outcome", "feedback"}},
		{"W02 signed promote resume", JobPublishBranches, "release:promote", true, "", "refs/heads/main", []string{"validate_request", "publish_branches", "wait_branch_tests", "publish_tags", "record_outcome", "feedback"}},
		{"W03 complete verify feedback", JobFeedback, "release:release", true, "", "refs/heads/main", []string{"validate_request", "feedback"}},
		{"W04 validation fails closed", "", "release:prepare", false, "", "refs/heads/main", []string{"validate_request"}},
		{"W05 tests-passed release resume", JobPublishTags, "release:release", true, "", "refs/heads/main", []string{"validate_request", "publish_tags", "observe_images", "record_outcome", "feedback"}},
		{"W06 exact no-record redispatch", JobReserveOperation, "release:promote", true, "", "refs/heads/main", []string{"validate_request", "reserve_operation", "generate_unsigned", "validate_sign_store", "publish_branches", "wait_branch_tests", "publish_tags", "record_outcome", "feedback"}},
		{"W07 wrong-ref direct dispatch", JobReserveOperation, "release:prepare", true, "", "refs/heads/not-main", nil},
		{"W08 failed predecessor stops mutation chain", JobReserveOperation, "release:prepare", true, "reserve_operation", "refs/heads/main", []string{"validate_request", "reserve_operation", "feedback"}},
		{"prepare tags-published resume records outcome", JobEnsurePreparePR, "release:prepare", true, "", "refs/heads/main", []string{"validate_request", "ensure_prepare_pr", "record_outcome", "feedback"}},
		{"release tags-published resume records outcome", JobObserveImages, "release:release", true, "", "refs/heads/main", []string{"validate_request", "observe_images", "record_outcome", "feedback"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			values := workflowOutputFlags(test.next, test.command)
			values["github.repository"] = RepositoryFullName
			values["github.event_name"] = "workflow_dispatch"
			values["github.ref"] = test.ref
			results := map[string]string{}
			var got []string
			for _, job := range jobOrder {
				for dependency, result := range results {
					values["needs."+dependency+".result"] = result
				}
				run, err := evaluateWorkflowCondition(conditions[job], values)
				if err != nil {
					t.Fatalf("evaluate %s condition %q: %v", job, conditions[job], err)
				}
				if run {
					got = append(got, job)
					if (job == "validate_request" && !test.validationSucceeds) || job == test.failJob {
						results[job] = "failure"
					} else {
						results[job] = "success"
					}
				} else {
					results[job] = "skipped"
				}
			}
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("jobs = %v, want %v", got, test.want)
			}
		})
	}
}

func evaluateActualWorkflowAttempt(t *testing.T, conditions map[string]string, values map[string]string, forcedResults map[string]string) []string {
	t.Helper()
	jobOrder := []string{"validate_request", "reserve_operation", "generate_unsigned", "validate_sign_store", "publish_branches", "wait_branch_tests", "publish_tags", "ensure_prepare_pr", "observe_images", "record_outcome", "feedback"}
	results := map[string]string{}
	for job, result := range forcedResults {
		results[job] = result
	}
	var scheduled []string
	for _, job := range jobOrder {
		if _, forced := forcedResults[job]; forced {
			continue
		}
		for dependency, result := range results {
			values["needs."+dependency+".result"] = result
		}
		run, err := evaluateWorkflowCondition(conditions[job], values)
		if err != nil {
			t.Fatalf("evaluate %s condition %q: %v", job, conditions[job], err)
		}
		if run {
			scheduled = append(scheduled, job)
			results[job] = "success"
		} else {
			results[job] = "skipped"
		}
	}
	return scheduled
}

func TestWorkflowYAMLPendingReplacementIsInertUntilExactRedispatchW06(t *testing.T) {
	conditions := workflowJobConditions(releaseWorkflowText(t))
	for _, validationResult := range []string{"cancelled", "skipped"} {
		t.Run(validationResult+" before lock", func(t *testing.T) {
			values := workflowOutputFlags(JobReserveOperation, "release:promote")
			values["github.repository"], values["github.event_name"], values["github.ref"] = RepositoryFullName, "workflow_dispatch", "refs/heads/main"
			if got := evaluateActualWorkflowAttempt(t, conditions, values, map[string]string{"validate_request": validationResult}); len(got) != 0 {
				t.Fatalf("replaced pre-lock attempt scheduled jobs: %v", got)
			}
		})
	}

	policyRaw := validPolicyJSON()
	requestRaw := validDispatchJSON(policyRaw)
	inspection, err := InspectReconciliation(requestRaw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !inspection.OK || inspection.Category != ReconcileAcknowledgedNoRecord {
		t.Fatalf("no-record inspection = %#v", inspection)
	}
	inputsRaw, err := json.Marshal(inspection.Inputs)
	if err != nil {
		t.Fatal(err)
	}
	var inputFields map[string]json.RawMessage
	if err := json.Unmarshal(inputsRaw, &inputFields); err != nil || len(inputFields) != 4 {
		t.Fatalf("retry inputs are not the exact four-field wire contract: %s err=%v", inputsRaw, err)
	}
	var contextFields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(inspection.Inputs.Context), &contextFields); err != nil || len(contextFields) != 7 {
		t.Fatalf("retry context = %s, err=%v", inspection.Inputs.Context, err)
	}
	retryRequest, err := DecodeDispatchInputs(map[string]string{"contract_version": inspection.Inputs.ContractVersion, "command": inspection.Inputs.Command, "version": inspection.Inputs.Version, "context": inspection.Inputs.Context})
	if err != nil {
		t.Fatal(err)
	}
	validated := ValidatedRequest{RequestKey: retryRequest.RequestKey, RequestSHA256: retryRequest.RequestSHA256, Command: retryRequest.Command, Version: retryRequest.Version, PolicyRevision: retryRequest.Context.PolicyRevision}
	retrySelector, err := SelectOperation(validated, LoadResult{RemoteHead: strings.Repeat("c", 40)})
	if err != nil || retrySelector.NextJob != JobReserveOperation || !retrySelector.RunReserve {
		t.Fatalf("exact redispatch selector = %#v, err=%v", retrySelector, err)
	}
	retryValues := workflowOutputFlags(retrySelector.NextJob, retrySelector.Command)
	retryValues["github.repository"], retryValues["github.event_name"], retryValues["github.ref"] = RepositoryFullName, "workflow_dispatch", "refs/heads/main"
	retryJobs := evaluateActualWorkflowAttempt(t, conditions, retryValues, nil)
	if !containsString(retryJobs, "reserve_operation") {
		t.Fatalf("exact redispatch did not schedule deterministic reservation: %v", retryJobs)
	}

	reservation := baseReservation()
	reservation.RequestKey = retryRequest.RequestKey
	reservation.RequestSHA256 = retryRequest.RequestSHA256
	reservation.RepositoryID = retryRequest.Context.RepositoryID
	reservation.RepositoryFullName = retryRequest.Context.RepositoryFullName
	reservation.IssueNumber = retryRequest.Context.IssueNumber
	reservation.OriginalCommentID = retryRequest.Context.OriginalCommentID
	reservation.AcknowledgementCommentID = retryRequest.Context.AcknowledgementCommentID
	reservation.Command = retryRequest.Command
	reservation.RequestedVersion = retryRequest.Version
	reservation.BodySnapshot = retryRequest.Context.BodySnapshot
	reservation.PolicyRevision = retryRequest.Context.PolicyRevision
	record := semanticSignedRecord(t, reservation)
	resolvedVersion := record.Reservation.ResolvedVersion
	resumeSelector, err := SelectOperation(validated, LoadResult{Found: true, RemoteHead: strings.Repeat("d", 40), Record: record})
	if err != nil || resumeSelector.NextJob != JobPublishBranches || resumeSelector.RunReserve || resumeSelector.Record.Reservation.ResolvedVersion != resolvedVersion {
		t.Fatalf("existing-record replacement selector = %#v, err=%v", resumeSelector, err)
	}
	resumeValues := workflowOutputFlags(resumeSelector.NextJob, resumeSelector.Command)
	resumeValues["github.repository"], resumeValues["github.event_name"], resumeValues["github.ref"] = RepositoryFullName, "workflow_dispatch", "refs/heads/main"
	resumeJobs := evaluateActualWorkflowAttempt(t, conditions, resumeValues, nil)
	for _, forbidden := range []string{"reserve_operation", "generate_unsigned", "validate_sign_store"} {
		if containsString(resumeJobs, forbidden) {
			t.Fatalf("existing signed record reran %s instead of resuming: %v", forbidden, resumeJobs)
		}
	}
	if !containsString(resumeJobs, "publish_branches") {
		t.Fatalf("existing signed record did not resume at publish_branches: %v", resumeJobs)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestWorkflowYAMLCancelledNeedStopsMutationButRunsFeedback(t *testing.T) {
	conditions := workflowJobConditions(releaseWorkflowText(t))
	values := workflowOutputFlags(JobGenerateUnsigned, "release:prepare")
	values["needs.validate_request.result"] = "success"
	values["needs.reserve_operation.result"] = "cancelled"
	run, err := evaluateWorkflowCondition(conditions["generate_unsigned"], values)
	if err != nil || run {
		t.Fatalf("cancelled reservation scheduled generation: run=%v err=%v", run, err)
	}
	values["needs.generate_unsigned.result"] = "skipped"
	values["needs.validate_sign_store.result"] = "skipped"
	values["needs.publish_branches.result"] = "skipped"
	values["needs.wait_branch_tests.result"] = "skipped"
	values["needs.publish_tags.result"] = "skipped"
	values["needs.ensure_prepare_pr.result"] = "skipped"
	values["needs.observe_images.result"] = "skipped"
	values["needs.record_outcome.result"] = "skipped"
	run, err = evaluateWorkflowCondition(conditions["feedback"], values)
	if err != nil || !run {
		t.Fatalf("cancelled chain suppressed feedback: run=%v err=%v", run, err)
	}
}

func TestWorkflowYAMLResumeConditionsW02W03W05W06(t *testing.T) {
	text := releaseWorkflowText(t)
	for _, token := range []string{
		"needs.reserve_operation.result == 'skipped' && needs.validate_request.outputs.next_job == 'generate_unsigned'",
		"needs.validate_sign_store.result == 'skipped' && needs.validate_request.outputs.next_job == 'publish_branches'",
		"needs.publish_branches.result == 'skipped' && needs.validate_request.outputs.next_job == 'wait_branch_tests'",
		"needs.wait_branch_tests.result == 'skipped' && needs.validate_request.outputs.next_job == 'publish_tags'",
		"needs.publish_tags.result == 'skipped' && needs.validate_request.outputs.next_job == 'ensure_prepare_pr'",
		"needs.publish_tags.result == 'skipped' && needs.validate_request.outputs.next_job == 'observe_images'",
		"needs.validate_request.outputs.command == 'release:prepare' && needs.ensure_prepare_pr.result == 'success'",
		"needs.validate_request.outputs.command == 'release:release' && needs.observe_images.result == 'success'",
		"needs.publish_tags.result == 'skipped' && needs.validate_request.outputs.next_job == 'record_outcome'",
		"if: ${{ always() && needs.validate_request.result == 'success' }}",
		"--selector \"$RUNNER_TEMP/selector/gardener-release-validate_request-v1.json\"",
	} {
		if !strings.Contains(text, token) {
			t.Errorf("resume graph missing %q", token)
		}
	}
	if strings.Count(text, "always()") < 9 {
		t.Fatal("skipped dependencies can suppress resume jobs")
	}
}
