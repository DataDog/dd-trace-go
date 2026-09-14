// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const repositoryAPIPath = "/repos/DataDog/dd-trace-go"

var (
	productionReleaseBranch = regexp.MustCompile(`^release-v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.x$`)
	productionDevBranch     = regexp.MustCompile(`^dev-v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.x$`)
)

type githubWorkflowRun struct {
	ID         json.RawMessage `json:"id"`
	RunAttempt int             `json:"run_attempt"`
	Event      string          `json:"event"`
	HeadBranch string          `json:"head_branch"`
	HeadSHA    string          `json:"head_sha"`
	Status     string          `json:"status"`
	Conclusion string          `json:"conclusion"`
	Path       string          `json:"path"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	WorkflowID json.RawMessage `json:"workflow_id"`
}

func (c *GitHubClient) ListWorkflowRunsPage(ctx context.Context, workflowPath string, page, perPage int) ([]WorkflowRun, bool, error) {
	if workflowPath != MainBranchTestWorkflowPath || page <= 0 || page > MaxGitHubPages || perPage <= 0 || perPage > GitHubPageSize {
		return nil, false, newReleaseError(ErrorClassContractMismatch, "invalid_actions_query")
	}
	path := repositoryAPIPath + "/actions/workflows/main-branch-tests.yml/runs"
	query := url.Values{"event": {"push"}, "page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}}
	body, header, err := c.getBounded(ctx, c.urlForPath(path, query), path)
	if err != nil {
		return nil, false, err
	}
	var response struct {
		WorkflowRuns []githubWorkflowRun `json:"workflow_runs"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, false, typedContractError("invalid_api_json")
	}
	runs := make([]WorkflowRun, 0, len(response.WorkflowRuns))
	for _, raw := range response.WorkflowRuns {
		run, err := convertWorkflowRun(raw)
		if err != nil {
			return nil, false, err
		}
		runs = append(runs, run)
	}
	return runs, hasNextLink(header.Get("Link")), nil
}

func (c *GitHubClient) ListWorkflowJobsPage(ctx context.Context, runID string, attempt, page, perPage int) ([]WorkflowJob, bool, error) {
	if !validID(runID) || attempt <= 0 || page <= 0 || page > MaxGitHubPages || perPage <= 0 || perPage > GitHubPageSize {
		return nil, false, newReleaseError(ErrorClassContractMismatch, "invalid_actions_query")
	}
	path := fmt.Sprintf("%s/actions/runs/%s/attempts/%d/jobs", repositoryAPIPath, runID, attempt)
	query := url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}}
	body, header, err := c.getBounded(ctx, c.urlForPath(path, query), path)
	if err != nil {
		return nil, false, err
	}
	var response struct {
		Jobs []struct {
			ID         json.RawMessage `json:"id"`
			Name       string          `json:"name"`
			Status     string          `json:"status"`
			Conclusion string          `json:"conclusion"`
			RunAttempt int             `json:"run_attempt"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, false, typedContractError("invalid_api_json")
	}
	jobs := make([]WorkflowJob, 0, len(response.Jobs))
	for _, raw := range response.Jobs {
		id, err := decodeGitHubID(raw.ID)
		if err != nil {
			return nil, false, err
		}
		jobs = append(jobs, WorkflowJob{ID: id, Name: raw.Name, Attempt: raw.RunAttempt, Status: raw.Status, Conclusion: raw.Conclusion})
	}
	return jobs, hasNextLink(header.Get("Link")), nil
}

func (c *GitHubClient) GetWorkflowRun(ctx context.Context, runID string) (WorkflowRun, error) {
	if !validID(runID) {
		return WorkflowRun{}, typedRequestError("unsafe_id")
	}
	path := repositoryAPIPath + "/actions/runs/" + runID
	body, _, err := c.getBounded(ctx, c.urlForPath(path, nil), path)
	if err != nil {
		return WorkflowRun{}, err
	}
	var raw githubWorkflowRun
	if err := json.Unmarshal(body, &raw); err != nil {
		return WorkflowRun{}, typedContractError("invalid_api_json")
	}
	return convertWorkflowRun(raw)
}

func convertWorkflowRun(raw githubWorkflowRun) (WorkflowRun, error) {
	id, err := decodeGitHubID(raw.ID)
	if err != nil {
		return WorkflowRun{}, err
	}
	workflowID := ""
	if len(raw.WorkflowID) > 0 && string(raw.WorkflowID) != "null" {
		workflowID, err = decodeGitHubID(raw.WorkflowID)
		if err != nil {
			return WorkflowRun{}, err
		}
	}
	return WorkflowRun{ID: id, RepositoryFullName: raw.Repository.FullName, WorkflowID: workflowID, WorkflowPath: normalizeWorkflowPath(raw.Path), Event: raw.Event, HeadBranch: raw.HeadBranch, HeadSHA: raw.HeadSHA, Attempt: raw.RunAttempt, Status: raw.Status, Conclusion: raw.Conclusion}, nil
}

func normalizeWorkflowPath(path string) string {
	if index := strings.Index(path, "@"); index >= 0 {
		path = path[:index]
	}
	return path
}

func (c *GitHubClient) ReadWorkflowFile(ctx context.Context, workflowPath, ref string) ([]byte, error) {
	allowed := map[string]string{
		MainBranchTestWorkflowPath: ".github/workflows/main-branch-tests.yml",
		ImageWorkflowPath:          ".github/workflows/docker-images-release.yml",
		ImageChildWorkflowPath:     ".github/workflows/docker-build-and-push.yml",
	}
	file, ok := allowed[workflowPath]
	if !ok || !ValidGitObjectID(ref) {
		return nil, newReleaseError(ErrorClassContractMismatch, "invalid_workflow_file_query")
	}
	path := repositoryAPIPath + "/contents/" + file
	body, _, err := c.getBounded(ctx, c.urlForPath(path, url.Values{"ref": {ref}}), path)
	if err != nil {
		return nil, err
	}
	var response struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
		SHA      string `json:"sha"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Encoding != "base64" || !ValidGitObjectID(response.SHA) {
		return nil, typedContractError("invalid_api_json")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(response.Content, "\n", ""))
	if err != nil || int64(len(decoded)) > c.maxBytes {
		return nil, typedContractError("invalid_api_json")
	}
	return decoded, nil
}

func (c *GitHubClient) ReadBranchRef(ctx context.Context, ref string) (string, bool, error) {
	if !validReleaseBranchRef(ref) {
		return "", false, newReleaseError(ErrorClassContractMismatch, "invalid_branch_ref")
	}
	path := repositoryAPIPath + "/git/ref/" + strings.TrimPrefix(ref, "refs/")
	body, _, err := c.getBounded(ctx, c.urlForPath(path, nil), path)
	if err != nil {
		if ErrorCode(err) == "not_found" {
			return "", false, nil
		}
		return "", false, err
	}
	var response struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.Ref != ref || response.Object.Type != "commit" || !ValidGitObjectID(response.Object.SHA) {
		return "", false, typedContractError("invalid_api_json")
	}
	return response.Object.SHA, true, nil
}

func validReleaseBranchRef(ref string) bool {
	name := strings.TrimPrefix(ref, "refs/heads/")
	return strings.HasPrefix(ref, "refs/heads/") && (productionReleaseBranch.MatchString(name) || productionDevBranch.MatchString(name))
}

func (c *GitHubClient) ListPullRequestsPage(ctx context.Context, page, perPage int) ([]PreparePullRequest, bool, error) {
	if page <= 0 || page > MaxGitHubPages || perPage <= 0 || perPage > GitHubPageSize {
		return nil, false, newReleaseError(ErrorClassContractMismatch, "invalid_pr_query")
	}
	path := repositoryAPIPath + "/pulls"
	body, header, err := c.getBounded(ctx, c.urlForPath(path, url.Values{"state": {"open"}, "page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}}), path)
	if err != nil {
		return nil, false, err
	}
	var raw []struct {
		Number  json.RawMessage `json:"number"`
		State   string          `json:"state"`
		Body    string          `json:"body"`
		HTMLURL string          `json:"html_url"`
		Head    struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo struct {
				FullName string `json:"full_name"`
			} `json:"repo"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, typedContractError("invalid_api_json")
	}
	result := make([]PreparePullRequest, 0, len(raw))
	for _, item := range raw {
		number, err := decodeGitHubID(item.Number)
		if err != nil {
			return nil, false, err
		}
		result = append(result, PreparePullRequest{Number: number, RepositoryFullName: item.Head.Repo.FullName, URL: item.HTMLURL, HeadRef: item.Head.Ref, HeadSHA: item.Head.SHA, BaseRef: item.Base.Ref, State: item.State, Body: item.Body})
	}
	return result, hasNextLink(header.Get("Link")), nil
}

func (c *GitHubClient) ListPullRequestFilesPage(ctx context.Context, number string, page, perPage int) ([]string, bool, error) {
	if !validID(number) || page <= 0 || page > MaxGitHubPages || perPage <= 0 || perPage > GitHubPageSize {
		return nil, false, newReleaseError(ErrorClassContractMismatch, "invalid_pr_query")
	}
	path := repositoryAPIPath + "/pulls/" + number + "/files"
	body, header, err := c.getBounded(ctx, c.urlForPath(path, url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}}), path)
	if err != nil {
		return nil, false, err
	}
	var raw []struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, typedContractError("invalid_api_json")
	}
	files := make([]string, 0, len(raw))
	for _, item := range raw {
		if !validRepositoryRelativePath(item.Filename) {
			return nil, false, newReleaseError(ErrorClassEvidenceIncomplete, "invalid_pr_file_path")
		}
		files = append(files, item.Filename)
	}
	return files, hasNextLink(header.Get("Link")), nil
}

func (c *GitHubClient) CreatePullRequest(ctx context.Context, request CreatePreparePullRequest) error {
	if !productionDevBranch.MatchString(request.HeadRef) || request.BaseRef != "main" || request.Title == "" || len(request.Title) > 256 || request.Body == "" || len(request.Body) > MaxContextBytes {
		return newReleaseError(ErrorClassContractMismatch, "invalid_prepare_pr")
	}
	path := repositoryAPIPath + "/pulls"
	payload, err := json.Marshal(struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
	}{request.Title, request.HeadRef, request.BaseRef, request.Body})
	if err != nil {
		return err
	}
	return c.mutateJSONOnce(ctx, http.MethodPost, path, payload)
}

func (c *GitHubClient) mutateJSONOnce(ctx context.Context, method, path string, payload []byte) error {
	ctx, cancel := c.boundedOperationContext(ctx)
	defer cancel()
	if !strings.HasPrefix(path, repositoryAPIPath+"/") || len(payload) > MaxContextBytes {
		return newReleaseError(ErrorClassContractMismatch, "invalid_api_mutation")
	}
	request, err := http.NewRequestWithContext(ctx, method, c.urlForPath(path, nil).String(), bytes.NewReader(payload))
	if err != nil {
		return wrapReleaseError(ErrorClassPublicationPartial, "mutation_request_build_failed", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return wrapReleaseError(ErrorClassPublicationPartial, "mutation_transport_error", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return newReleaseError(ErrorClassPublicationPartial, "mutation_unexpected_status")
	}
	written, err := io.Copy(io.Discard, io.LimitReader(response.Body, c.maxBytes+1))
	if err != nil || written > c.maxBytes {
		return newReleaseError(ErrorClassPublicationPartial, "mutation_response_invalid")
	}
	return nil
}

func (c *GitHubClient) ListImageWorkflowRunsPage(ctx context.Context, moduleTag string, page, perPage int) ([]WorkflowRun, bool, error) {
	validTag := false
	for _, item := range imagePackages {
		if strings.HasPrefix(moduleTag, item.ModulePrefix) {
			_, err := parseImageVersion(strings.TrimPrefix(moduleTag, item.ModulePrefix))
			validTag = err == nil
		}
	}
	if !validTag || page <= 0 || page > MaxGitHubPages || perPage <= 0 || perPage > GitHubPageSize {
		return nil, false, newReleaseError(ErrorClassContractMismatch, "invalid_image_run_query")
	}
	path := repositoryAPIPath + "/actions/workflows/docker-images-release.yml/runs"
	query := url.Values{"event": {"push"}, "branch": {moduleTag}, "page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}}
	body, header, err := c.getBounded(ctx, c.urlForPath(path, query), path)
	if err != nil {
		return nil, false, err
	}
	var response struct {
		WorkflowRuns []githubWorkflowRun `json:"workflow_runs"`
	}
	if json.Unmarshal(body, &response) != nil {
		return nil, false, typedContractError("invalid_api_json")
	}
	runs := make([]WorkflowRun, 0, len(response.WorkflowRuns))
	for _, raw := range response.WorkflowRuns {
		run, err := convertWorkflowRun(raw)
		if err != nil {
			return nil, false, err
		}
		runs = append(runs, run)
	}
	return runs, hasNextLink(header.Get("Link")), nil
}

func (c *GitHubClient) ListRunArtifactsPage(ctx context.Context, runID string, page, perPage int) ([]ImageRunArtifact, bool, error) {
	if !validID(runID) || page <= 0 || page > MaxGitHubPages || perPage <= 0 || perPage > GitHubPageSize {
		return nil, false, newReleaseError(ErrorClassContractMismatch, "invalid_artifact_query")
	}
	path := repositoryAPIPath + "/actions/runs/" + runID + "/artifacts"
	body, header, err := c.getBounded(ctx, c.urlForPath(path, url.Values{"page": {strconv.Itoa(page)}, "per_page": {strconv.Itoa(perPage)}}), path)
	if err != nil {
		return nil, false, err
	}
	var response struct {
		Artifacts []struct {
			ID      json.RawMessage `json:"id"`
			Name    string          `json:"name"`
			Size    int64           `json:"size_in_bytes"`
			Digest  string          `json:"digest"`
			Expired bool            `json:"expired"`
		} `json:"artifacts"`
	}
	if json.Unmarshal(body, &response) != nil {
		return nil, false, typedContractError("invalid_api_json")
	}
	items := make([]ImageRunArtifact, 0, len(response.Artifacts))
	for _, raw := range response.Artifacts {
		id, err := decodeGitHubID(raw.ID)
		if err != nil {
			return nil, false, err
		}
		items = append(items, ImageRunArtifact{ID: id, Name: raw.Name, Size: raw.Size, Digest: raw.Digest, Expired: raw.Expired})
	}
	return items, hasNextLink(header.Get("Link")), nil
}

func (c *GitHubClient) DownloadImageResultArtifact(ctx context.Context, artifactID string) ([]byte, error) {
	ctx, cancel := c.boundedOperationContext(ctx)
	defer cancel()
	if !validID(artifactID) {
		return nil, typedRequestError("unsafe_id")
	}
	path := repositoryAPIPath + "/actions/artifacts/" + artifactID + "/zip"
	endpoint := c.urlForPath(path, nil)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "artifact_request_failed", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "artifact_download_failed", err)
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		location := response.Header.Get("Location")
		response.Body.Close()
		redirect, parseErr := url.Parse(location)
		if parseErr != nil || redirect.User != nil || (redirect.Scheme != "https" && !(redirect.Scheme == c.baseURL.Scheme && redirect.Host == c.baseURL.Host)) {
			return nil, newReleaseError(ErrorClassEvidenceIncomplete, "artifact_redirect_invalid")
		}
		request, err = http.NewRequestWithContext(ctx, http.MethodGet, redirect.String(), nil)
		if err != nil {
			return nil, newReleaseError(ErrorClassEvidenceIncomplete, "artifact_redirect_invalid")
		}
		response, err = c.httpClient.Do(request)
		if err != nil {
			return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "artifact_download_failed", err)
		}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "artifact_download_failed")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxGitHubResponseBytes+1))
	if err != nil || len(body) == 0 || len(body) > MaxGitHubResponseBytes {
		return nil, newReleaseError(ErrorClassEvidenceIncomplete, "artifact_download_invalid")
	}
	return body, nil
}

func (c *GitHubClient) ReadImageWorkflowFile(ctx context.Context, ref string) ([]byte, error) {
	return c.readImageWorkflowFile(ctx, ref, "docker-images-release.yml")
}

func (c *GitHubClient) ReadImageChildWorkflowFile(ctx context.Context, ref string) ([]byte, error) {
	return c.readImageWorkflowFile(ctx, ref, "docker-build-and-push.yml")
}

func (c *GitHubClient) readImageWorkflowFile(ctx context.Context, ref, name string) ([]byte, error) {
	if !ValidGitObjectID(ref) || (name != "docker-images-release.yml" && name != "docker-build-and-push.yml") {
		return nil, newReleaseError(ErrorClassContractMismatch, "invalid_workflow_file_query")
	}
	path := repositoryAPIPath + "/contents/.github/workflows/" + name
	body, _, err := c.getBounded(ctx, c.urlForPath(path, url.Values{"ref": {ref}}), path)
	if err != nil {
		return nil, err
	}
	var response struct{ Encoding, Content, SHA string }
	if json.Unmarshal(body, &response) != nil || response.Encoding != "base64" || !ValidGitObjectID(response.SHA) {
		return nil, typedContractError("invalid_api_json")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(response.Content, "\n", ""))
	if err != nil || int64(len(decoded)) > c.maxBytes {
		return nil, typedContractError("invalid_api_json")
	}
	return decoded, nil
}

func (c *GitHubClient) ReadImageSourceTag(ctx context.Context, ref string) (ImageSourceTag, bool, error) {
	if !validFullTagRef(ref) {
		return ImageSourceTag{}, false, newReleaseError(ErrorClassContractMismatch, "invalid_image_tag_ref")
	}
	valid := false
	for _, item := range imagePackages {
		if strings.HasPrefix(ref, "refs/tags/"+item.ModulePrefix) {
			valid = true
		}
	}
	if !valid {
		return ImageSourceTag{}, false, newReleaseError(ErrorClassContractMismatch, "invalid_image_tag_ref")
	}
	path := repositoryAPIPath + "/git/ref/" + strings.TrimPrefix(ref, "refs/")
	body, _, err := c.getBounded(ctx, c.urlForPath(path, nil), path)
	if err != nil {
		if ErrorCode(err) == "not_found" {
			return ImageSourceTag{}, false, nil
		}
		return ImageSourceTag{}, false, err
	}
	var response struct {
		Ref    string                     `json:"ref"`
		Object struct{ SHA, Type string } `json:"object"`
	}
	if json.Unmarshal(body, &response) != nil || response.Ref != ref || response.Object.Type != "tag" || !ValidGitObjectID(response.Object.SHA) {
		return ImageSourceTag{}, false, typedContractError("invalid_api_json")
	}
	tagPath := repositoryAPIPath + "/git/tags/" + response.Object.SHA
	tagBody, _, err := c.getBounded(ctx, c.urlForPath(tagPath, nil), tagPath)
	if err != nil {
		return ImageSourceTag{}, false, err
	}
	var tag struct {
		Object struct{ SHA, Type string } `json:"object"`
	}
	if json.Unmarshal(tagBody, &tag) != nil || tag.Object.Type != "commit" || !ValidGitObjectID(tag.Object.SHA) || tag.Object.SHA == response.Object.SHA {
		return ImageSourceTag{}, false, typedContractError("invalid_api_json")
	}
	return ImageSourceTag{Ref: ref, ObjectSHA: response.Object.SHA, CommitSHA: tag.Object.SHA}, true, nil
}

func hasNextLink(value string) bool {
	return strings.Contains(value, `rel="next"`)
}

func validRepositoryRelativePath(path string) bool {
	return path != "" && !strings.HasPrefix(path, "/") && !strings.Contains(path, "\\") && !strings.Contains(path, "\x00") && !strings.Contains(path, "../") && path != ".."
}

var _ ChecksAPI = (*GitHubClient)(nil)
var _ PreparePRAPI = (*GitHubClient)(nil)
