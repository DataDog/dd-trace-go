// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	ImageWorkflowPath           = ".github/workflows/docker-images-release.yml"
	ImageLatestConcurrencyGroup = "gardener-image-latest-production-v1"
)

// ImagePackage is one reviewed module-tag to registry-package mapping.
type ImagePackage struct {
	ModulePrefix string `json:"module_prefix"`
	Image        string `json:"image"`
}

var imagePackages = []ImagePackage{
	{ModulePrefix: "contrib/azure/apim-callout/", Image: "ghcr.io/datadog/dd-trace-go/apim-callout"},
	{ModulePrefix: "contrib/envoyproxy/go-control-plane/", Image: "ghcr.io/datadog/dd-trace-go/service-extensions-callout"},
	{ModulePrefix: "contrib/k8s.io/gateway-api/", Image: "ghcr.io/datadog/dd-trace-go/request-mirror"},
	{ModulePrefix: "contrib/haproxy/stream-processing-offload/", Image: "ghcr.io/datadog/dd-trace-go/haproxy-spoa"},
}

// ImagePromotionRequest contains immutable workflow/build identity. Values are
// independently re-read through ImagePromotionAPI before any latest mutation.
type ImagePromotionRequest struct {
	RepositoryFullName string
	Event              string
	WorkflowPath       string
	RunID              string
	RunAttempt         int
	ModuleTagRef       string
	ModuleTagObjectSHA string
	CommitSHA          string
	Image              string
	Version            string
	VersionDigest      string
	BuildStatus        string
	BuildConclusion    string
}

type ImageWorkflowRun struct {
	RepositoryFullName string
	Event              string
	WorkflowPath       string
	RunID              string
	Attempt            int
	HeadSHA            string
	Status             string
	Conclusion         string
}

type ImageSourceTag struct {
	Ref       string
	ObjectSHA string
	CommitSHA string
}

type ImageManifest struct {
	Image              string
	Digest             string
	Version            string
	CommitSHA          string
	ModuleTagObject    string
	RepositoryFullName string
	WorkflowPath       string
	RunID              string
	RunAttempt         int
}

// ImagePromotionAPI separates bounded source/workflow/registry reads from the
// single latest mutation. Implementations must page root tags completely and
// return the canonical first run for the module-tag push.
type ImagePromotionAPI interface {
	GetImageWorkflowRun(context.Context, string) (ImageWorkflowRun, error)
	CanonicalImageRunID(context.Context, string) (string, error)
	ReadImageSourceTag(context.Context, string) (ImageSourceTag, bool, error)
	ListRootGATagsPage(context.Context, int, int) ([]ImageSourceTag, bool, error)
	ReadImageManifest(context.Context, string, string) (ImageManifest, bool, error)
	PromoteImageLatest(context.Context, string, string) error
}

type ImageOutcome string

const (
	ImagePromoted    ImageOutcome = "promoted"
	ImageReconciled  ImageOutcome = "reconciled"
	ImageVersionOnly ImageOutcome = "versioned_only"
	ImagePending     ImageOutcome = "pending"
	ImageFailed      ImageOutcome = "failed"
)

type ImagePromotionEvidence struct {
	SchemaVersion        string       `json:"schema_version"`
	Outcome              ImageOutcome `json:"outcome"`
	RepositoryFullName   string       `json:"repository_full_name"`
	WorkflowPath         string       `json:"workflow_path"`
	RunID                string       `json:"run_id"`
	RunAttempt           int          `json:"run_attempt"`
	Event                string       `json:"event"`
	BuildStatus          string       `json:"build_status"`
	BuildConclusion      string       `json:"build_conclusion"`
	ModuleTagRef         string       `json:"module_tag_ref"`
	ModuleTagObjectSHA   string       `json:"module_tag_object_sha"`
	CommitSHA            string       `json:"commit_sha"`
	Version              string       `json:"version"`
	Image                string       `json:"image"`
	VersionDigest        string       `json:"version_digest"`
	RootTagObjectSHA     string       `json:"root_tag_object_sha,omitempty"`
	PreviousLatestDigest string       `json:"previous_latest_digest,omitempty"`
}

type imageVersion struct {
	major, minor, patch int
	docker              int
	hasDocker           bool
}

func parseImageVersion(value string) (imageVersion, error) {
	var result imageVersion
	base := value
	if i := strings.Index(value, "-docker."); i >= 0 {
		base = value[:i]
		suffix := value[i+len("-docker."):]
		n, err := canonicalImageNumber(suffix)
		if err != nil {
			return result, err
		}
		result.docker, result.hasDocker = n, true
	}
	if strings.ContainsAny(base, "+-") || !strings.HasPrefix(base, "v") {
		return result, newReleaseError(ErrorClassContractMismatch, "invalid_image_version")
	}
	parts := strings.Split(strings.TrimPrefix(base, "v"), ".")
	if len(parts) != 3 {
		return result, newReleaseError(ErrorClassContractMismatch, "invalid_image_version")
	}
	values := []*int{&result.major, &result.minor, &result.patch}
	for i, part := range parts {
		n, err := canonicalImageNumber(part)
		if err != nil {
			return result, err
		}
		*values[i] = n
	}
	return result, nil
}

func canonicalImageNumber(value string) (int, error) {
	if value == "" || len(value) > 10 || len(value) > 1 && value[0] == '0' {
		return 0, newReleaseError(ErrorClassContractMismatch, "invalid_image_version")
	}
	n, err := strconv.ParseInt(value, 10, 32)
	if err != nil || n < 0 {
		return 0, newReleaseError(ErrorClassContractMismatch, "invalid_image_version")
	}
	return int(n), nil
}

func compareImageVersion(a, b imageVersion) int {
	av, bv := []int{a.major, a.minor, a.patch}, []int{b.major, b.minor, b.patch}
	for i := range av {
		if av[i] < bv[i] {
			return -1
		}
		if av[i] > bv[i] {
			return 1
		}
	}
	return 0
}

func imagePackageForRequest(request ImagePromotionRequest) (ImagePackage, imageVersion, error) {
	version, err := parseImageVersion(request.Version)
	if err != nil {
		return ImagePackage{}, version, err
	}
	for _, candidate := range imagePackages {
		if request.Image == candidate.Image && request.ModuleTagRef == "refs/tags/"+candidate.ModulePrefix+request.Version {
			return candidate, version, nil
		}
	}
	return ImagePackage{}, version, newReleaseError(ErrorClassContractMismatch, "image_package_mismatch")
}

func validImageDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && lowerHexDigest(strings.TrimPrefix(value, "sha256:"))
}

type imageBuildState int

const (
	imageBuildInvalid imageBuildState = iota
	imageBuildPending
	imageBuildFailed
	imageBuildSuccessful
)

func classifyImageBuild(status, conclusion string) imageBuildState {
	switch status {
	case "queued", "in_progress", "pending", "requested", "waiting":
		if conclusion == "" {
			return imageBuildPending
		}
	case "completed":
		switch conclusion {
		case "success":
			return imageBuildSuccessful
		case "failure", "cancelled", "timed_out", "action_required", "startup_failure", "stale", "neutral", "skipped":
			return imageBuildFailed
		}
	}
	return imageBuildInvalid
}

// ObserveAndPromoteImage applies the shared four-package policy and mutates
// latest at most once. Every successful mutation is followed by an exact
// digest/provenance read; a lost response is reconciled only from that read.
func ObserveAndPromoteImage(ctx context.Context, api ImagePromotionAPI, request ImagePromotionRequest) (ImagePromotionEvidence, error) {
	evidence := ImagePromotionEvidence{SchemaVersion: "1", RepositoryFullName: request.RepositoryFullName, WorkflowPath: request.WorkflowPath, RunID: request.RunID, RunAttempt: request.RunAttempt, Event: request.Event, BuildStatus: request.BuildStatus, BuildConclusion: request.BuildConclusion, ModuleTagRef: request.ModuleTagRef, ModuleTagObjectSHA: request.ModuleTagObjectSHA, CommitSHA: request.CommitSHA, Version: request.Version, Image: request.Image, VersionDigest: request.VersionDigest}
	imagePackage, version, err := imagePackageForRequest(request)
	if err != nil {
		return evidence, err
	}
	if request.RepositoryFullName != RepositoryFullName || request.WorkflowPath != ImageWorkflowPath || !validID(request.RunID) || request.RunAttempt <= 0 || !ValidGitObjectID(request.ModuleTagObjectSHA) || !ValidGitObjectID(request.CommitSHA) || request.ModuleTagObjectSHA == request.CommitSHA {
		return evidence, newReleaseError(ErrorClassContractMismatch, "invalid_image_provenance")
	}
	switch classifyImageBuild(request.BuildStatus, request.BuildConclusion) {
	case imageBuildPending:
		if request.VersionDigest != "" {
			return evidence, newReleaseError(ErrorClassContractMismatch, "inconsistent_image_build_evidence")
		}
		evidence.Outcome = ImagePending
		return evidence, nil
	case imageBuildFailed:
		if request.VersionDigest != "" {
			return evidence, newReleaseError(ErrorClassContractMismatch, "inconsistent_image_build_evidence")
		}
		evidence.Outcome = ImageFailed
		return evidence, nil
	case imageBuildSuccessful:
		if !validImageDigest(request.VersionDigest) {
			return evidence, newReleaseError(ErrorClassContractMismatch, "invalid_image_digest")
		}
	default:
		return evidence, newReleaseError(ErrorClassContractMismatch, "invalid_image_build_state")
	}
	// Manual, docker rebuild and rerun paths can publish their immutable
	// version only. They can never enter the latest mutation seam.
	if request.Event != "push" || version.hasDocker || request.RunAttempt != 1 {
		evidence.Outcome = ImageVersionOnly
		return evidence, nil
	}
	if api == nil {
		return evidence, newReleaseError(ErrorClassEvidenceIncomplete, "image_api_unavailable")
	}
	run, err := api.GetImageWorkflowRun(ctx, request.RunID)
	if err != nil {
		return evidence, wrapReleaseError(ErrorClassEvidenceIncomplete, "image_run_read_failed", err)
	}
	if run.RepositoryFullName != request.RepositoryFullName || run.WorkflowPath != request.WorkflowPath || run.Event != request.Event || run.RunID != request.RunID || run.Attempt != request.RunAttempt || run.HeadSHA != request.CommitSHA || run.Status != "completed" || run.Conclusion != "success" {
		return evidence, newReleaseError(ErrorClassEvidenceIncomplete, "image_run_mismatch")
	}
	canonicalRun, err := api.CanonicalImageRunID(ctx, request.ModuleTagRef)
	if err != nil {
		return evidence, wrapReleaseError(ErrorClassEvidenceIncomplete, "canonical_image_run_read_failed", err)
	}
	if canonicalRun != request.RunID {
		evidence.Outcome = ImageVersionOnly
		return evidence, nil
	}
	moduleTag, found, err := api.ReadImageSourceTag(ctx, request.ModuleTagRef)
	if err != nil {
		return evidence, wrapReleaseError(ErrorClassEvidenceIncomplete, "module_tag_read_failed", err)
	}
	if !found || moduleTag.Ref != request.ModuleTagRef || moduleTag.ObjectSHA != request.ModuleTagObjectSHA || moduleTag.CommitSHA != request.CommitSHA || moduleTag.ObjectSHA == moduleTag.CommitSHA {
		return evidence, newReleaseError(ErrorClassEvidenceIncomplete, "module_tag_mismatch")
	}
	rootTags, err := allRootGATags(ctx, api)
	if err != nil {
		return evidence, err
	}
	candidateRoot := "refs/tags/" + request.Version
	highest, highestFound, candidateFound := imageVersion{}, false, false
	for _, tag := range rootTags {
		if !strings.HasPrefix(tag.Ref, "refs/tags/v") {
			continue
		}
		parsed, parseErr := parseImageVersion(strings.TrimPrefix(tag.Ref, "refs/tags/"))
		if parseErr != nil || parsed.hasDocker {
			continue
		}
		if !ValidGitObjectID(tag.ObjectSHA) || !ValidGitObjectID(tag.CommitSHA) || tag.ObjectSHA == tag.CommitSHA {
			return evidence, newReleaseError(ErrorClassEvidenceIncomplete, "root_tag_provenance_invalid")
		}
		if !highestFound || compareImageVersion(parsed, highest) > 0 {
			highest, highestFound = parsed, true
		}
		if tag.Ref == candidateRoot && tag.CommitSHA == request.CommitSHA {
			candidateFound = true
			evidence.RootTagObjectSHA = tag.ObjectSHA
		}
	}
	if !candidateFound || !highestFound {
		return evidence, newReleaseError(ErrorClassEvidenceIncomplete, "root_tag_mismatch")
	}
	if compareImageVersion(version, highest) != 0 {
		return evidence, newReleaseError(ErrorClassStateConflict, "image_version_not_highest_ga")
	}
	candidate, found, err := api.ReadImageManifest(ctx, request.Image, request.Version)
	if err != nil {
		return evidence, wrapReleaseError(ErrorClassEvidenceIncomplete, "versioned_image_read_failed", err)
	}
	if !found || !manifestMatches(candidate, request) {
		return evidence, newReleaseError(ErrorClassEvidenceIncomplete, "versioned_image_provenance_mismatch")
	}
	latest, latestFound, err := api.ReadImageManifest(ctx, request.Image, "latest")
	if err != nil {
		return evidence, wrapReleaseError(ErrorClassEvidenceIncomplete, "latest_image_read_failed", err)
	}
	if latestFound {
		evidence.PreviousLatestDigest = latest.Digest
		latestVersion, verifyErr := verifyExistingLatest(ctx, api, imagePackage, latest)
		if verifyErr != nil {
			return evidence, verifyErr
		}
		if manifestMatches(latest, request) {
			evidence.Outcome = ImageReconciled
			return evidence, nil
		}
		cmp := compareImageVersion(latestVersion, version)
		if cmp >= 0 { // Same-GA different digest and newer-GA both fail closed.
			return evidence, newReleaseError(ErrorClassStateConflict, "latest_image_not_replaceable")
		}
	}
	promoteErr := api.PromoteImageLatest(ctx, request.Image, request.VersionDigest)
	post, postFound, readErr := api.ReadImageManifest(ctx, request.Image, "latest")
	if readErr != nil {
		return evidence, wrapReleaseError(ErrorClassPublicationPartial, "latest_image_postread_failed", readErr)
	}
	if postFound && manifestMatches(post, request) {
		if promoteErr != nil {
			evidence.Outcome = ImageReconciled
		} else {
			evidence.Outcome = ImagePromoted
		}
		return evidence, nil
	}
	if promoteErr != nil {
		return evidence, wrapReleaseError(ErrorClassPublicationPartial, "latest_image_promotion_unconfirmed", promoteErr)
	}
	return evidence, newReleaseError(ErrorClassPublicationPartial, "latest_image_promotion_unconfirmed")
}

func verifyExistingLatest(ctx context.Context, api ImagePromotionAPI, imagePackage ImagePackage, latest ImageManifest) (imageVersion, error) {
	version, err := parseImageVersion(latest.Version)
	if err != nil || version.hasDocker || latest.Image != imagePackage.Image || !validImageDigest(latest.Digest) || !ValidGitObjectID(latest.CommitSHA) || !ValidGitObjectID(latest.ModuleTagObject) || latest.CommitSHA == latest.ModuleTagObject || latest.RepositoryFullName != RepositoryFullName || latest.WorkflowPath != ImageWorkflowPath || !validID(latest.RunID) || latest.RunAttempt != 1 {
		return imageVersion{}, newReleaseError(ErrorClassStateConflict, "latest_image_provenance_mismatch")
	}
	moduleRef := "refs/tags/" + imagePackage.ModulePrefix + latest.Version
	moduleTag, found, err := api.ReadImageSourceTag(ctx, moduleRef)
	if err != nil {
		return imageVersion{}, wrapReleaseError(ErrorClassEvidenceIncomplete, "latest_module_tag_read_failed", err)
	}
	if !found || moduleTag.Ref != moduleRef || moduleTag.ObjectSHA != latest.ModuleTagObject || moduleTag.CommitSHA != latest.CommitSHA || moduleTag.ObjectSHA == moduleTag.CommitSHA {
		return imageVersion{}, newReleaseError(ErrorClassStateConflict, "latest_module_tag_mismatch")
	}
	rootRef := "refs/tags/" + latest.Version
	rootTag, found, err := api.ReadImageSourceTag(ctx, rootRef)
	if err != nil {
		return imageVersion{}, wrapReleaseError(ErrorClassEvidenceIncomplete, "latest_root_tag_read_failed", err)
	}
	if !found || rootTag.Ref != rootRef || !ValidGitObjectID(rootTag.ObjectSHA) || rootTag.ObjectSHA == rootTag.CommitSHA || rootTag.CommitSHA != latest.CommitSHA {
		return imageVersion{}, newReleaseError(ErrorClassStateConflict, "latest_root_tag_mismatch")
	}
	run, err := api.GetImageWorkflowRun(ctx, latest.RunID)
	if err != nil {
		return imageVersion{}, wrapReleaseError(ErrorClassEvidenceIncomplete, "latest_image_run_read_failed", err)
	}
	if run.RepositoryFullName != latest.RepositoryFullName || run.WorkflowPath != latest.WorkflowPath || run.Event != "push" || run.RunID != latest.RunID || run.Attempt != latest.RunAttempt || run.HeadSHA != latest.CommitSHA || run.Status != "completed" || run.Conclusion != "success" {
		return imageVersion{}, newReleaseError(ErrorClassStateConflict, "latest_image_run_mismatch")
	}
	canonicalRun, err := api.CanonicalImageRunID(ctx, moduleRef)
	if err != nil {
		return imageVersion{}, wrapReleaseError(ErrorClassEvidenceIncomplete, "latest_canonical_run_read_failed", err)
	}
	if canonicalRun != latest.RunID {
		return imageVersion{}, newReleaseError(ErrorClassStateConflict, "latest_image_run_not_canonical")
	}
	return version, nil
}

func manifestMatches(manifest ImageManifest, request ImagePromotionRequest) bool {
	return manifest.Image == request.Image && manifest.Digest == request.VersionDigest && manifest.Version == request.Version && manifest.CommitSHA == request.CommitSHA && manifest.ModuleTagObject == request.ModuleTagObjectSHA && manifest.RepositoryFullName == request.RepositoryFullName && manifest.WorkflowPath == request.WorkflowPath && manifest.RunID == request.RunID && manifest.RunAttempt == request.RunAttempt
}

func allRootGATags(ctx context.Context, api ImagePromotionAPI) ([]ImageSourceTag, error) {
	var tags []ImageSourceTag
	for page := 1; ; page++ {
		if page > MaxGitHubPages {
			return nil, newReleaseError(ErrorClassEvidenceIncomplete, "pagination_exhausted")
		}
		items, next, err := api.ListRootGATagsPage(ctx, page, GitHubPageSize)
		if err != nil {
			return nil, wrapReleaseError(ErrorClassEvidenceIncomplete, "root_tags_read_failed", err)
		}
		tags = append(tags, items...)
		if !next {
			return tags, nil
		}
	}
}

func validateImagePromotionEvidence(evidence ImagePromotionEvidence) error {
	request := ImagePromotionRequest{
		RepositoryFullName: evidence.RepositoryFullName,
		Event:              evidence.Event,
		WorkflowPath:       evidence.WorkflowPath,
		RunID:              evidence.RunID,
		RunAttempt:         evidence.RunAttempt,
		ModuleTagRef:       evidence.ModuleTagRef,
		ModuleTagObjectSHA: evidence.ModuleTagObjectSHA,
		CommitSHA:          evidence.CommitSHA,
		Image:              evidence.Image,
		Version:            evidence.Version,
		VersionDigest:      evidence.VersionDigest,
		BuildStatus:        evidence.BuildStatus,
		BuildConclusion:    evidence.BuildConclusion,
	}
	_, version, err := imagePackageForRequest(request)
	if err != nil {
		return err
	}
	if evidence.SchemaVersion != "1" || evidence.RepositoryFullName != RepositoryFullName || evidence.WorkflowPath != ImageWorkflowPath || !validID(evidence.RunID) || evidence.RunAttempt <= 0 || !ValidGitObjectID(evidence.ModuleTagObjectSHA) || !ValidGitObjectID(evidence.CommitSHA) || evidence.ModuleTagObjectSHA == evidence.CommitSHA || evidence.Event != "push" && evidence.Event != "workflow_dispatch" {
		return newReleaseError(ErrorClassStateConflict, "invalid_image_evidence")
	}
	state := classifyImageBuild(evidence.BuildStatus, evidence.BuildConclusion)
	switch evidence.Outcome {
	case ImagePending:
		if state != imageBuildPending || evidence.VersionDigest != "" || evidence.RootTagObjectSHA != "" || evidence.PreviousLatestDigest != "" {
			return newReleaseError(ErrorClassStateConflict, "inconsistent_image_evidence")
		}
	case ImageFailed:
		if state != imageBuildFailed || evidence.VersionDigest != "" || evidence.RootTagObjectSHA != "" || evidence.PreviousLatestDigest != "" {
			return newReleaseError(ErrorClassStateConflict, "inconsistent_image_evidence")
		}
	case ImageVersionOnly:
		if state != imageBuildSuccessful || !validImageDigest(evidence.VersionDigest) || evidence.RootTagObjectSHA != "" || evidence.PreviousLatestDigest != "" {
			return newReleaseError(ErrorClassStateConflict, "inconsistent_image_evidence")
		}
	case ImagePromoted, ImageReconciled:
		if state != imageBuildSuccessful || evidence.Event != "push" || version.hasDocker || evidence.RunAttempt != 1 || !validImageDigest(evidence.VersionDigest) || !ValidGitObjectID(evidence.RootTagObjectSHA) {
			return newReleaseError(ErrorClassStateConflict, "inconsistent_image_evidence")
		}
		if evidence.PreviousLatestDigest != "" && !validImageDigest(evidence.PreviousLatestDigest) {
			return newReleaseError(ErrorClassStateConflict, "invalid_image_evidence")
		}
	default:
		return newReleaseError(ErrorClassStateConflict, "invalid_image_evidence")
	}
	return nil
}

// RecordImageOutcome validates and appends immutable image evidence without
// advancing or rewriting Git release state. An exact prior event is idempotent;
// conflicting or malformed evidence is never overwritten.
func RecordImageOutcome(record Record, evidence ImagePromotionEvidence) (Record, error) {
	if err := validateImagePromotionEvidence(evidence); err != nil {
		return Record{}, err
	}
	if err := VerifyEventChain(record.Reservation.RequestKey, record.Events); err != nil {
		return Record{}, err
	}
	for _, event := range record.Events {
		if event.Kind != EventImageObserved {
			continue
		}
		var existing ImagePromotionEvidence
		if err := json.Unmarshal(event.Evidence, &existing); err != nil {
			return Record{}, newReleaseError(ErrorClassStateConflict, "invalid_recorded_image_evidence")
		}
		if err := validateImagePromotionEvidence(existing); err != nil {
			return Record{}, newReleaseError(ErrorClassStateConflict, "invalid_recorded_image_evidence")
		}
		if existing == evidence {
			return record, nil
		}
	}
	body, err := json.Marshal(evidence)
	if err != nil {
		return Record{}, err
	}
	updated := record
	updated.Events, err = AppendEvent(record.Events, record.Reservation.RequestKey, EventImageObserved, body)
	return updated, err
}

// ImagePackagePolicy returns a stable copy for workflow/static-policy tests.
func ImagePackagePolicy() []ImagePackage {
	result := append([]ImagePackage(nil), imagePackages...)
	sort.Slice(result, func(i, j int) bool { return result[i].ModulePrefix < result[j].ModulePrefix })
	return result
}

func (e ImagePromotionEvidence) String() string {
	return fmt.Sprintf("%s:%s@%s", e.Outcome, e.Image, e.VersionDigest)
}
