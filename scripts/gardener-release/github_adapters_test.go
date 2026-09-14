// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGitHubChecksAdapterUsesFixedBoundedPaths(t *testing.T) {
	sha := strings.Repeat("a", 40)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case repositoryAPIPath + "/actions/workflows/main-branch-tests.yml/runs":
			fmt.Fprintf(w, `{"workflow_runs":[{"id":10,"run_attempt":1,"event":"push","head_branch":"release-v2.9.x","head_sha":"%s","status":"completed","conclusion":"success","path":".github/workflows/main-branch-tests.yml@%s","workflow_id":20,"repository":{"full_name":"DataDog/dd-trace-go"}}]}`, sha, sha)
		case repositoryAPIPath + "/contents/.github/workflows/main-branch-tests.yml",
			repositoryAPIPath + "/contents/.github/workflows/docker-images-release.yml",
			repositoryAPIPath + "/contents/.github/workflows/docker-build-and-push.yml":
			fmt.Fprintf(w, `{"encoding":"base64","content":"%s","sha":"%s"}`, base64.StdEncoding.EncodeToString([]byte("name: tests\n")), sha)
		case repositoryAPIPath + "/git/ref/heads/release-v2.9.x":
			fmt.Fprintf(w, `{"ref":"refs/heads/release-v2.9.x","object":{"type":"commit","sha":"%s"}}`, sha)
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := newTestGitHubClient(server.URL, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	runs, next, err := client.ListWorkflowRunsPage(context.Background(), MainBranchTestWorkflowPath, 1, 100)
	if err != nil || next || len(runs) != 1 || runs[0].HeadSHA != sha || runs[0].WorkflowPath != MainBranchTestWorkflowPath {
		t.Fatalf("runs=%#v next=%v err=%v", runs, next, err)
	}
	for _, path := range []string{MainBranchTestWorkflowPath, ImageWorkflowPath, ImageChildWorkflowPath} {
		workflow, err := client.ReadWorkflowFile(context.Background(), path, sha)
		if err != nil || string(workflow) != "name: tests\n" {
			t.Fatalf("path=%s workflow=%q err=%v", path, workflow, err)
		}
	}
	if _, err := client.ReadWorkflowFile(context.Background(), ".github/workflows/attacker.yml", sha); ErrorCode(err) != "invalid_workflow_file_query" {
		t.Fatalf("arbitrary workflow error = %q", ErrorCode(err))
	}
	got, found, err := client.ReadBranchRef(context.Background(), "refs/heads/release-v2.9.x")
	if err != nil || !found || got != sha {
		t.Fatalf("ref=%q found=%v err=%v", got, found, err)
	}
}

func TestGitHubImageObserverAdaptersUseFixedRunArtifactAndTagPaths(t *testing.T) {
	sha := strings.Repeat("a", 40)
	tagObject := strings.Repeat("b", 40)
	archive := []byte("zip")
	redirectAuthorized := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case repositoryAPIPath + "/actions/workflows/docker-images-release.yml/runs":
			if r.URL.Query().Get("event") != "push" || r.URL.Query().Get("branch") != "contrib/azure/apim-callout/v2.9.0" {
				t.Fatalf("query=%s", r.URL.RawQuery)
			}
			fmt.Fprintf(w, `{"workflow_runs":[{"id":10,"run_attempt":1,"event":"push","head_branch":"contrib/azure/apim-callout/v2.9.0","head_sha":"%s","status":"completed","conclusion":"success","path":".github/workflows/docker-images-release.yml","workflow_id":30,"repository":{"full_name":"DataDog/dd-trace-go"}}]}`, sha)
		case repositoryAPIPath + "/actions/runs/10/artifacts":
			_, _ = w.Write([]byte(`{"artifacts":[{"id":70,"name":"gardener-release-image-result-v1","size_in_bytes":3,"digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","expired":false}]}`))
		case repositoryAPIPath + "/actions/artifacts/70/zip":
			http.Redirect(w, r, serverURL(r)+"/archive", http.StatusFound)
		case "/archive":
			redirectAuthorized = r.Header.Get("Authorization") != ""
			_, _ = w.Write(archive)
		case repositoryAPIPath + "/git/ref/tags/contrib/azure/apim-callout/v2.9.0":
			fmt.Fprintf(w, `{"ref":"refs/tags/contrib/azure/apim-callout/v2.9.0","object":{"type":"tag","sha":"%s"}}`, tagObject)
		case repositoryAPIPath + "/git/tags/" + tagObject:
			fmt.Fprintf(w, `{"object":{"type":"commit","sha":"%s"}}`, sha)
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := newTestGitHubClient(server.URL, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	runs, _, err := client.ListImageWorkflowRunsPage(context.Background(), "contrib/azure/apim-callout/v2.9.0", 1, 100)
	if err != nil || len(runs) != 1 || runs[0].WorkflowID != "30" {
		t.Fatalf("runs=%#v err=%v", runs, err)
	}
	artifacts, _, err := client.ListRunArtifactsPage(context.Background(), "10", 1, 100)
	if err != nil || len(artifacts) != 1 || artifacts[0].ID != "70" {
		t.Fatalf("artifacts=%#v err=%v", artifacts, err)
	}
	got, err := client.DownloadImageResultArtifact(context.Background(), "70")
	if err != nil || string(got) != "zip" || redirectAuthorized {
		t.Fatalf("archive=%q auth=%v err=%v", got, redirectAuthorized, err)
	}
	tag, found, err := client.ReadImageSourceTag(context.Background(), "refs/tags/contrib/azure/apim-callout/v2.9.0")
	if err != nil || !found || tag.ObjectSHA != tagObject || tag.CommitSHA != sha {
		t.Fatalf("tag=%#v found=%v err=%v", tag, found, err)
	}
}

func serverURL(r *http.Request) string { return "http://" + r.Host }

func TestImageArtifactDownloadHasIndependentDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := newTestGitHubClient(server.URL, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	client.readDeadline = 20 * time.Millisecond
	started := time.Now()
	if _, err := client.DownloadImageResultArtifact(context.Background(), "70"); err == nil {
		t.Fatal("stalled artifact download unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("artifact deadline took %s", elapsed)
	}
}

func TestGitHubPreparePRMutationRunsOnce(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != repositoryAPIPath+"/pulls" {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		http.Error(w, "lost", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client, err := newTestGitHubClient(server.URL, server.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = client.CreatePullRequest(context.Background(), CreatePreparePullRequest{Title: "chore: prepare", HeadRef: "dev-v2.10.x", BaseRef: "main", Body: "<!-- gardener:release:prepare-pr:v1:123:789 -->"})
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
