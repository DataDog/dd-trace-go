// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

type fakeImageObservationAPI struct {
	runs      map[string][]WorkflowRun
	artifacts map[string][]ImageRunArtifact
	archives  map[string][]byte
	tags      map[string]ImageSourceTag
	workflow  []byte
}

func (f *fakeImageObservationAPI) ListImageWorkflowRunsPage(_ context.Context, tag string, page, _ int) ([]WorkflowRun, bool, error) {
	if page == 1 {
		return f.runs[tag], false, nil
	}
	return nil, false, nil
}
func (f *fakeImageObservationAPI) ListRunArtifactsPage(_ context.Context, run string, page, _ int) ([]ImageRunArtifact, bool, error) {
	if page == 1 {
		return f.artifacts[run], false, nil
	}
	return nil, false, nil
}
func (f *fakeImageObservationAPI) DownloadImageResultArtifact(_ context.Context, id string) ([]byte, error) {
	return f.archives[id], nil
}
func (f *fakeImageObservationAPI) ReadImageWorkflowFile(_ context.Context, _ string) ([]byte, error) {
	return f.workflow, nil
}
func (f *fakeImageObservationAPI) ReadImageChildWorkflowFile(_ context.Context, _ string) ([]byte, error) {
	return f.workflow, nil
}
func (f *fakeImageObservationAPI) ReadImageSourceTag(_ context.Context, ref string) (ImageSourceTag, bool, error) {
	tag, ok := f.tags[ref]
	return tag, ok, nil
}

func imageResultArchive(t *testing.T, document imageResultDocument) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, err := writer.Create(ImageResultFileName)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(document)
	if _, err := file.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func observationFixture(t *testing.T) (Record, ImageObservationPolicy, *fakeImageObservationAPI) {
	t.Helper()
	record := checksRecord("release:release")
	record.Phase = PhaseTagsPublished
	record.Reservation.ResolvedVersion = "v2.9.0"
	workflow := []byte("name: fixed image workflow\n")
	workflowDigest := sha256.Sum256(workflow)
	policy := ImageObservationPolicy{WorkflowID: "30", WorkflowPath: ImageWorkflowPath, WorkflowSHA256: hex.EncodeToString(workflowDigest[:]), ChildWorkflowSHA256: hex.EncodeToString(workflowDigest[:])}
	api := &fakeImageObservationAPI{runs: map[string][]WorkflowRun{}, artifacts: map[string][]ImageRunArtifact{}, archives: map[string][]byte{}, tags: map[string]ImageSourceTag{}, workflow: workflow}
	for i, item := range ImagePackagePolicy() {
		runID := string(rune('1' + i))
		moduleTag := item.ModulePrefix + "v2.9.0"
		tagObject := strings.Repeat(string(rune('a'+i)), 40)
		rootObject := strings.Repeat("f", 40)
		run := WorkflowRun{ID: runID, RepositoryFullName: RepositoryFullName, WorkflowID: "30", WorkflowPath: ImageWorkflowPath, Event: "push", HeadBranch: moduleTag, HeadSHA: record.SignedOutput.ReleaseSHA, Attempt: 1, Status: "completed", Conclusion: "success"}
		api.runs[moduleTag] = []WorkflowRun{run}
		ref := "refs/tags/" + moduleTag
		api.tags[ref] = ImageSourceTag{Ref: ref, ObjectSHA: tagObject, CommitSHA: record.SignedOutput.ReleaseSHA}
		document := imageResultDocument{SchemaVersion: "1", RepositoryFullName: RepositoryFullName, WorkflowID: "30", WorkflowPath: ImageWorkflowPath, WorkflowSHA256: policy.WorkflowSHA256, ChildWorkflowSHA256: policy.ChildWorkflowSHA256, Event: "push", RunID: runID, RunAttempt: 1, BuildStatus: "completed", BuildConclusion: "success", ModuleTagRef: ref, ModuleTagObjectSHA: tagObject, CommitSHA: record.SignedOutput.ReleaseSHA, Version: "v2.9.0", Image: item.Image, VersionDigest: "sha256:" + strings.Repeat(string(rune('a'+i)), 64), RootTagObjectSHA: rootObject, PromotionOutcome: ImagePromoted}
		archive := imageResultArchive(t, document)
		sum := sha256.Sum256(archive)
		artifactID := strconv.Itoa(70 + i)
		api.artifacts[runID] = []ImageRunArtifact{{ID: artifactID, Name: ImageResultArtifactName, Size: int64(len(archive)), Digest: "sha256:" + hex.EncodeToString(sum[:])}}
		api.archives[artifactID] = archive
	}
	return record, policy, api
}

func TestObserveReleaseImagesBindsFourFixedSuccessfulArtifacts(t *testing.T) {
	record, policy, api := observationFixture(t)
	evidence, outcome, err := ObserveReleaseImages(context.Background(), api, policy, record)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != WorkSucceeded || len(evidence) != 4 {
		t.Fatalf("outcome=%s evidence=%#v", outcome, evidence)
	}
}

func TestObserveReleaseImagesRejectsHostileArtifactAndDuplicateRun(t *testing.T) {
	t.Run("duplicate run", func(t *testing.T) {
		record, policy, api := observationFixture(t)
		for key, runs := range api.runs {
			api.runs[key] = append(runs, runs[0])
			break
		}
		if _, _, err := ObserveReleaseImages(context.Background(), api, policy, record); ErrorCode(err) != "image_run_ambiguous" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	})
	t.Run("expired artifact", func(t *testing.T) {
		record, policy, api := observationFixture(t)
		for key, items := range api.artifacts {
			items[0].Expired = true
			api.artifacts[key] = items
			break
		}
		if _, _, err := ObserveReleaseImages(context.Background(), api, policy, record); ErrorCode(err) != "image_result_artifact_invalid" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	})
	t.Run("digest mismatch", func(t *testing.T) {
		record, policy, api := observationFixture(t)
		for key, items := range api.artifacts {
			items[0].Digest = "sha256:" + strings.Repeat("0", 64)
			api.artifacts[key] = items
			break
		}
		if _, _, err := ObserveReleaseImages(context.Background(), api, policy, record); ErrorCode(err) != "image_result_digest_mismatch" {
			t.Fatalf("error=%q", ErrorCode(err))
		}
	})
}

func TestObserveReleaseImagesPendingAndFailedCarryNoInventedDigest(t *testing.T) {
	for _, state := range []struct {
		status, conclusion string
		want               WorkOutcome
	}{{"in_progress", "", WorkPending}, {"completed", "failure", WorkFailed}} {
		record, policy, api := observationFixture(t)
		for key, runs := range api.runs {
			runs[0].Status, runs[0].Conclusion = state.status, state.conclusion
			api.runs[key] = runs
			break
		}
		evidence, outcome, err := ObserveReleaseImages(context.Background(), api, policy, record)
		if err != nil {
			t.Fatal(err)
		}
		if outcome != state.want {
			t.Fatalf("outcome=%s want=%s", outcome, state.want)
		}
		for _, item := range evidence {
			if (item.Outcome == ImagePending || item.Outcome == ImageFailed) && item.VersionDigest != "" {
				t.Fatal("invented digest")
			}
		}
	}
}
