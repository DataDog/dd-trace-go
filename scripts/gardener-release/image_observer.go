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
	"io"
	"sort"
)

const (
	ImageResultArtifactName = "gardener-release-image-result-v1"
	ImageResultFileName     = "gardener-release-image-result-v1.json"
	MaxImageResultBytes     = 64 * 1024
)

type ImageObservationPolicy struct {
	WorkflowID          string
	WorkflowPath        string
	WorkflowSHA256      string
	ChildWorkflowSHA256 string
}

type ImageRunArtifact struct {
	ID      string
	Name    string
	Size    int64
	Digest  string
	Expired bool
}

// ImageObservationAPI is read-only. It deliberately has no package write,
// latest promotion, Git, tag publication, or state mutation method.
type ImageObservationAPI interface {
	ListImageWorkflowRunsPage(context.Context, string, int, int) ([]WorkflowRun, bool, error)
	ListRunArtifactsPage(context.Context, string, int, int) ([]ImageRunArtifact, bool, error)
	DownloadImageResultArtifact(context.Context, string) ([]byte, error)
	ReadImageWorkflowFile(context.Context, string) ([]byte, error)
	ReadImageChildWorkflowFile(context.Context, string) ([]byte, error)
	ReadImageSourceTag(context.Context, string) (ImageSourceTag, bool, error)
}

type imageResultDocument struct {
	SchemaVersion       string       `json:"schema_version"`
	RepositoryFullName  string       `json:"repository_full_name"`
	WorkflowID          string       `json:"workflow_id"`
	WorkflowPath        string       `json:"workflow_path"`
	WorkflowSHA256      string       `json:"workflow_sha256"`
	ChildWorkflowSHA256 string       `json:"child_workflow_sha256"`
	Event               string       `json:"event"`
	RunID               string       `json:"run_id"`
	RunAttempt          int          `json:"run_attempt"`
	BuildStatus         string       `json:"build_status"`
	BuildConclusion     string       `json:"build_conclusion"`
	ModuleTagRef        string       `json:"module_tag_ref"`
	ModuleTagObjectSHA  string       `json:"module_tag_object_sha"`
	CommitSHA           string       `json:"commit_sha"`
	Version             string       `json:"version"`
	Image               string       `json:"image"`
	VersionDigest       string       `json:"version_digest"`
	RootTagObjectSHA    string       `json:"root_tag_object_sha"`
	PromotionOutcome    ImageOutcome `json:"promotion_outcome"`
}

// ObserveReleaseImages discovers exactly one run and one fixed result artifact
// for each fixed package. It never invokes B11's promotion seam.
func ObserveReleaseImages(ctx context.Context, api ImageObservationAPI, policy ImageObservationPolicy, record Record) ([]ImagePromotionEvidence, WorkOutcome, error) {
	if api == nil || record.Reservation.Command != "release:release" || record.Phase != PhaseTagsPublished || record.SignedOutput == nil || !validID(policy.WorkflowID) || policy.WorkflowPath != ImageWorkflowPath || !lowerHexDigest(policy.WorkflowSHA256) || !lowerHexDigest(policy.ChildWorkflowSHA256) {
		return nil, "", newReleaseError(ErrorClassEvidenceIncomplete, "image_observer_not_ready")
	}
	results := make([]ImagePromotionEvidence, 0, len(imagePackages))
	hasPending, hasFailed := false, false
	for _, item := range ImagePackagePolicy() {
		moduleTag := item.ModulePrefix + record.Reservation.ResolvedVersion
		run, state, err := discoverImageRun(ctx, api, policy, moduleTag, record.SignedOutput.ReleaseSHA)
		if err != nil {
			return nil, "", err
		}
		ref := "refs/tags/" + moduleTag
		tag, found, err := api.ReadImageSourceTag(ctx, ref)
		if err != nil || !found || tag.Ref != ref || !ValidGitObjectID(tag.ObjectSHA) || tag.ObjectSHA == tag.CommitSHA || tag.CommitSHA != record.SignedOutput.ReleaseSHA {
			return nil, "", newReleaseError(ErrorClassEvidenceIncomplete, "image_module_tag_mismatch")
		}
		if state == imageBuildPending || state == imageBuildFailed {
			outcome := ImagePending
			if state == imageBuildFailed {
				outcome, hasFailed = ImageFailed, true
			} else {
				hasPending = true
			}
			results = append(results, ImagePromotionEvidence{SchemaVersion: "1", Outcome: outcome, RepositoryFullName: RepositoryFullName, WorkflowPath: ImageWorkflowPath, WorkflowSHA256: policy.WorkflowSHA256, ChildWorkflowSHA256: policy.ChildWorkflowSHA256, RunID: run.ID, RunAttempt: run.Attempt, Event: "push", BuildStatus: run.Status, BuildConclusion: run.Conclusion, ModuleTagRef: ref, ModuleTagObjectSHA: tag.ObjectSHA, CommitSHA: tag.CommitSHA, Version: record.Reservation.ResolvedVersion, Image: item.Image})
			continue
		}
		document, err := readImageResult(ctx, api, run.ID)
		if err != nil {
			return nil, "", err
		}
		workflow, err := api.ReadImageWorkflowFile(ctx, run.HeadSHA)
		if err != nil {
			return nil, "", wrapReleaseError(ErrorClassEvidenceIncomplete, "image_workflow_read_failed", err)
		}
		childWorkflow, err := api.ReadImageChildWorkflowFile(ctx, run.HeadSHA)
		if err != nil {
			return nil, "", wrapReleaseError(ErrorClassEvidenceIncomplete, "image_child_workflow_read_failed", err)
		}
		digest := sha256.Sum256(workflow)
		childDigest := sha256.Sum256(childWorkflow)
		if hex.EncodeToString(digest[:]) != policy.WorkflowSHA256 || hex.EncodeToString(childDigest[:]) != policy.ChildWorkflowSHA256 || document.WorkflowSHA256 != policy.WorkflowSHA256 || document.ChildWorkflowSHA256 != policy.ChildWorkflowSHA256 || document.SchemaVersion != "1" || document.RepositoryFullName != RepositoryFullName || document.WorkflowID != policy.WorkflowID || document.WorkflowPath != policy.WorkflowPath || document.Event != "push" || document.RunID != run.ID || document.RunAttempt != run.Attempt || document.ModuleTagRef != ref || document.ModuleTagObjectSHA != tag.ObjectSHA || document.CommitSHA != tag.CommitSHA || document.Version != record.Reservation.ResolvedVersion || document.Image != item.Image || document.BuildStatus != run.Status || document.BuildConclusion != run.Conclusion {
			return nil, "", newReleaseError(ErrorClassEvidenceIncomplete, "image_result_mismatch")
		}
		evidence := ImagePromotionEvidence{SchemaVersion: "1", Outcome: document.PromotionOutcome, RepositoryFullName: document.RepositoryFullName, WorkflowPath: document.WorkflowPath, WorkflowSHA256: document.WorkflowSHA256, ChildWorkflowSHA256: document.ChildWorkflowSHA256, RunID: document.RunID, RunAttempt: document.RunAttempt, Event: document.Event, BuildStatus: document.BuildStatus, BuildConclusion: document.BuildConclusion, ModuleTagRef: document.ModuleTagRef, ModuleTagObjectSHA: document.ModuleTagObjectSHA, CommitSHA: document.CommitSHA, Version: document.Version, Image: document.Image, VersionDigest: document.VersionDigest, RootTagObjectSHA: document.RootTagObjectSHA}
		if validateImagePromotionEvidence(evidence) != nil {
			return nil, "", newReleaseError(ErrorClassEvidenceIncomplete, "invalid_image_result")
		}
		if evidence.Outcome != ImagePromoted && evidence.Outcome != ImageReconciled {
			return nil, "", newReleaseError(ErrorClassEvidenceIncomplete, "image_latest_outcome_required")
		}
		results = append(results, evidence)
	}
	status := WorkSucceeded
	if hasFailed {
		status = WorkFailed
	} else if hasPending {
		status = WorkPending
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Image < results[j].Image })
	return results, status, nil
}

func discoverImageRun(ctx context.Context, api ImageObservationAPI, policy ImageObservationPolicy, moduleTag, sha string) (WorkflowRun, imageBuildState, error) {
	var matches []WorkflowRun
	for page := 1; ; page++ {
		if page > MaxGitHubPages {
			return WorkflowRun{}, imageBuildInvalid, newReleaseError(ErrorClassEvidenceIncomplete, "pagination_exhausted")
		}
		runs, next, err := api.ListImageWorkflowRunsPage(ctx, moduleTag, page, GitHubPageSize)
		if err != nil {
			return WorkflowRun{}, imageBuildInvalid, wrapReleaseError(ErrorClassEvidenceIncomplete, "image_runs_read_failed", err)
		}
		for _, run := range runs {
			if run.RepositoryFullName == RepositoryFullName && run.WorkflowID == policy.WorkflowID && run.WorkflowPath == policy.WorkflowPath && run.Event == "push" && run.HeadBranch == moduleTag && run.HeadSHA == sha && run.Attempt > 0 {
				matches = append(matches, run)
			}
		}
		if !next {
			break
		}
	}
	if len(matches) != 1 {
		return WorkflowRun{}, imageBuildInvalid, newReleaseError(ErrorClassEvidenceIncomplete, "image_run_ambiguous")
	}
	state := classifyImageBuild(matches[0].Status, matches[0].Conclusion)
	if state == imageBuildInvalid {
		return WorkflowRun{}, state, newReleaseError(ErrorClassEvidenceIncomplete, "image_run_state_invalid")
	}
	return matches[0], state, nil
}

func readImageResult(ctx context.Context, api ImageObservationAPI, runID string) (imageResultDocument, error) {
	var matches []ImageRunArtifact
	for page := 1; ; page++ {
		if page > MaxGitHubPages {
			return imageResultDocument{}, newReleaseError(ErrorClassEvidenceIncomplete, "pagination_exhausted")
		}
		items, next, err := api.ListRunArtifactsPage(ctx, runID, page, GitHubPageSize)
		if err != nil {
			return imageResultDocument{}, wrapReleaseError(ErrorClassEvidenceIncomplete, "image_artifacts_read_failed", err)
		}
		for _, item := range items {
			if item.Name == ImageResultArtifactName {
				matches = append(matches, item)
			}
		}
		if !next {
			break
		}
	}
	if len(matches) != 1 || matches[0].Expired || !validID(matches[0].ID) || matches[0].Size <= 0 || matches[0].Size > MaxImageResultBytes || !validArtifactDigest(matches[0].Digest) {
		return imageResultDocument{}, newReleaseError(ErrorClassEvidenceIncomplete, "image_result_artifact_invalid")
	}
	archive, err := api.DownloadImageResultArtifact(ctx, matches[0].ID)
	if err != nil || len(archive) == 0 || len(archive) > MaxGitHubResponseBytes {
		return imageResultDocument{}, newReleaseError(ErrorClassEvidenceIncomplete, "image_result_download_failed")
	}
	sum := sha256.Sum256(archive)
	if matches[0].Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		return imageResultDocument{}, newReleaseError(ErrorClassEvidenceIncomplete, "image_result_digest_mismatch")
	}
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil || len(reader.File) != 1 || reader.File[0].Name != ImageResultFileName || reader.File[0].UncompressedSize64 == 0 || reader.File[0].UncompressedSize64 > MaxImageResultBytes {
		return imageResultDocument{}, newReleaseError(ErrorClassEvidenceIncomplete, "invalid_image_result_archive")
	}
	file, err := reader.File[0].Open()
	if err != nil {
		return imageResultDocument{}, newReleaseError(ErrorClassEvidenceIncomplete, "invalid_image_result_archive")
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, MaxImageResultBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(raw) == 0 || len(raw) > MaxImageResultBytes {
		return imageResultDocument{}, newReleaseError(ErrorClassEvidenceIncomplete, "invalid_image_result_archive")
	}
	if validateJSONNoDuplicateKeys(raw, MaxImageResultBytes) != nil {
		return imageResultDocument{}, newReleaseError(ErrorClassEvidenceIncomplete, "invalid_image_result")
	}
	var document imageResultDocument
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return imageResultDocument{}, newReleaseError(ErrorClassEvidenceIncomplete, "invalid_image_result")
	}
	return document, nil
}

func validArtifactDigest(value string) bool {
	return len(value) == len("sha256:")+64 && value[:7] == "sha256:" && lowerHexDigest(value[7:])
}
