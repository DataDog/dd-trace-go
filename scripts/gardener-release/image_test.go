// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeImageAPI struct {
	run         ImageWorkflowRun
	canonical   string
	module      ImageSourceTag
	roots       []ImageSourceTag
	runs        map[string]ImageWorkflowRun
	canonicals  map[string]string
	sourceTags  map[string]ImageSourceTag
	afterFirst  map[string]ImageSourceTag
	sourceReads map[string]int
	candidate   ImageManifest
	latest      ImageManifest
	hasLatest   bool
	promotes    int
	promoteErr  error
	runErr      error
	manifestErr error
	noApply     bool
	pageForever bool
}

func (f *fakeImageAPI) GetImageWorkflowRun(_ context.Context, id string) (ImageWorkflowRun, error) {
	if f.runErr != nil {
		return ImageWorkflowRun{}, f.runErr
	}
	if run, ok := f.runs[id]; ok {
		return run, nil
	}
	return f.run, nil
}
func (f *fakeImageAPI) CanonicalImageRunID(_ context.Context, ref string) (string, error) {
	if id, ok := f.canonicals[ref]; ok {
		return id, nil
	}
	return f.canonical, nil
}
func (f *fakeImageAPI) ReadImageSourceTag(_ context.Context, ref string) (ImageSourceTag, bool, error) {
	f.sourceReads[ref]++
	if f.sourceReads[ref] > 1 {
		if tag, ok := f.afterFirst[ref]; ok {
			return tag, true, nil
		}
	}
	if tag, ok := f.sourceTags[ref]; ok {
		return tag, true, nil
	}
	if f.module.Ref == ref {
		return f.module, true, nil
	}
	for _, tag := range f.roots {
		if tag.Ref == ref {
			return tag, true, nil
		}
	}
	return ImageSourceTag{}, false, nil
}
func (f *fakeImageAPI) ListRootGATagsPage(_ context.Context, page, _ int) ([]ImageSourceTag, bool, error) {
	if page == 1 {
		return f.roots, f.pageForever, nil
	}
	return nil, f.pageForever, nil
}
func (f *fakeImageAPI) ReadImageManifest(_ context.Context, _, ref string) (ImageManifest, bool, error) {
	if f.manifestErr != nil {
		return ImageManifest{}, false, f.manifestErr
	}
	if ref == "latest" {
		return f.latest, f.hasLatest, nil
	}
	return f.candidate, true, nil
}
func (f *fakeImageAPI) PromoteImageLatest(_ context.Context, _, _ string) error {
	f.promotes++
	if !f.noApply {
		f.latest, f.hasLatest = f.candidate, true
	}
	return f.promoteErr
}

func imageFixture() (ImagePromotionRequest, *fakeImageAPI) {
	commit := strings.Repeat("1", 40)
	tagObject := strings.Repeat("2", 40)
	digest := "sha256:" + strings.Repeat("a", 64)
	request := ImagePromotionRequest{
		RepositoryFullName: RepositoryFullName, Event: "push", WorkflowPath: ImageWorkflowPath,
		RunID: "100", RunAttempt: 1, ModuleTagRef: "refs/tags/contrib/azure/apim-callout/v2.10.0",
		ModuleTagObjectSHA: tagObject, CommitSHA: commit, Image: "ghcr.io/datadog/dd-trace-go/apim-callout",
		Version: "v2.10.0", VersionDigest: digest, BuildStatus: "completed", BuildConclusion: "success",
	}
	manifest := ImageManifest{Image: request.Image, Digest: digest, Version: request.Version, CommitSHA: commit, ModuleTagObject: tagObject, RepositoryFullName: request.RepositoryFullName, WorkflowPath: request.WorkflowPath, RunID: request.RunID, RunAttempt: request.RunAttempt}
	run := ImageWorkflowRun{RepositoryFullName: RepositoryFullName, Event: "push", WorkflowPath: ImageWorkflowPath, RunID: "100", Attempt: 1, HeadSHA: commit, Status: "completed", Conclusion: "success"}
	module := ImageSourceTag{Ref: request.ModuleTagRef, ObjectSHA: tagObject, CommitSHA: commit}
	root := ImageSourceTag{Ref: "refs/tags/v2.10.0", ObjectSHA: strings.Repeat("3", 40), CommitSHA: commit}
	api := &fakeImageAPI{
		run: run, canonical: "100", module: module, roots: []ImageSourceTag{root}, candidate: manifest,
		runs: map[string]ImageWorkflowRun{"100": run}, canonicals: map[string]string{request.ModuleTagRef: "100"},
		sourceTags: map[string]ImageSourceTag{request.ModuleTagRef: module, root.Ref: root}, afterFirst: map[string]ImageSourceTag{}, sourceReads: map[string]int{},
	}
	return request, api
}

func addHistoricalLatest(api *fakeImageAPI) {
	commit := strings.Repeat("8", 40)
	tagObject := strings.Repeat("9", 40)
	rootObject := strings.Repeat("a", 40)
	moduleRef := "refs/tags/contrib/azure/apim-callout/v2.9.0"
	api.latest = ImageManifest{
		Image: "ghcr.io/datadog/dd-trace-go/apim-callout", Digest: "sha256:" + strings.Repeat("b", 64),
		Version: "v2.9.0", CommitSHA: commit, ModuleTagObject: tagObject,
		RepositoryFullName: RepositoryFullName, WorkflowPath: ImageWorkflowPath, RunID: "90", RunAttempt: 1,
	}
	api.hasLatest = true
	api.sourceTags[moduleRef] = ImageSourceTag{Ref: moduleRef, ObjectSHA: tagObject, CommitSHA: commit}
	api.sourceTags["refs/tags/v2.9.0"] = ImageSourceTag{Ref: "refs/tags/v2.9.0", ObjectSHA: rootObject, CommitSHA: commit}
	api.runs["90"] = ImageWorkflowRun{RepositoryFullName: RepositoryFullName, Event: "push", WorkflowPath: ImageWorkflowPath, RunID: "90", Attempt: 1, HeadSHA: commit, Status: "completed", Conclusion: "success"}
	api.canonicals[moduleRef] = "90"
}

func TestImagePromotionI01OlderDelayedBuildCannotRegressLatest(t *testing.T) {
	request, api := imageFixture()
	api.roots = append(api.roots, ImageSourceTag{Ref: "refs/tags/v2.11.0", ObjectSHA: strings.Repeat("4", 40), CommitSHA: strings.Repeat("5", 40)})
	_, err := ObserveAndPromoteImage(context.Background(), api, request)
	if ErrorCode(err) != "image_version_not_highest_ga" || api.promotes != 0 {
		t.Fatalf("error=%q promotes=%d", ErrorCode(err), api.promotes)
	}
}

func TestImagePromotionI02ManualAndMismatchedProvenanceDenied(t *testing.T) {
	request, api := imageFixture()
	request.Event = "workflow_dispatch"
	outcome, err := ObserveAndPromoteImage(context.Background(), api, request)
	if err != nil || outcome.Outcome != ImageVersionOnly || api.promotes != 0 {
		t.Fatalf("outcome=%#v err=%v", outcome, err)
	}
	for name, mutate := range map[string]func(*ImagePromotionRequest){
		"package":    func(r *ImagePromotionRequest) { r.Image = "ghcr.io/example/wrong" },
		"sha":        func(r *ImagePromotionRequest) { r.CommitSHA = strings.Repeat("6", 40) },
		"tag object": func(r *ImagePromotionRequest) { r.ModuleTagObjectSHA = r.CommitSHA },
	} {
		t.Run(name, func(t *testing.T) {
			r, a := imageFixture()
			mutate(&r)
			if _, err := ObserveAndPromoteImage(context.Background(), a, r); err == nil || a.promotes != 0 {
				t.Fatalf("error=%v promotes=%d", err, a.promotes)
			}
		})
	}
}

func TestImagePromotionI03SameGARebuildAndDockerVersionOnly(t *testing.T) {
	request, api := imageFixture()
	api.hasLatest = true
	api.latest = api.candidate
	outcome, err := ObserveAndPromoteImage(context.Background(), api, request)
	if err != nil || outcome.Outcome != ImageReconciled || api.promotes != 0 {
		t.Fatalf("outcome=%#v err=%v", outcome, err)
	}

	request, api = imageFixture()
	api.hasLatest = true
	api.latest = api.candidate
	api.latest.Digest = "sha256:" + strings.Repeat("b", 64)
	if _, err := ObserveAndPromoteImage(context.Background(), api, request); ErrorCode(err) != "latest_image_not_replaceable" || api.promotes != 0 {
		t.Fatalf("error=%q promotes=%d", ErrorCode(err), api.promotes)
	}

	request, api = imageFixture()
	request.RunAttempt = 2
	outcome, err = ObserveAndPromoteImage(context.Background(), api, request)
	if err != nil || outcome.Outcome != ImageVersionOnly || api.promotes != 0 {
		t.Fatalf("outcome=%#v err=%v", outcome, err)
	}

	request, api = imageFixture()
	request.Version = "v2.10.0-docker.1"
	request.ModuleTagRef = "refs/tags/contrib/azure/apim-callout/" + request.Version
	outcome, err = ObserveAndPromoteImage(context.Background(), api, request)
	if err != nil || outcome.Outcome != ImageVersionOnly || api.promotes != 0 {
		t.Fatalf("outcome=%#v err=%v", outcome, err)
	}
}

func TestImagePromotionI04BuildFailureRecordsWithoutGitMutation(t *testing.T) {
	request, api := imageFixture()
	request.BuildConclusion = "failure"
	request.VersionDigest = ""
	outcome, err := ObserveAndPromoteImage(context.Background(), api, request)
	if err != nil || outcome.Outcome != ImageFailed || api.promotes != 0 {
		t.Fatalf("outcome=%#v err=%v", outcome, err)
	}
	record := Record{Reservation: baseReservation()}
	updated, err := RecordImageOutcome(record, outcome)
	if err != nil || len(updated.Events) != 1 || updated.Events[0].Kind != EventImageObserved {
		t.Fatalf("record=%#v err=%v", updated, err)
	}
}

func TestImagePromotionPendingAndFailedBuildsNeedNoDigest(t *testing.T) {
	for _, tc := range []struct {
		name, status, conclusion string
		want                     ImageOutcome
	}{
		{name: "queued", status: "queued", want: ImagePending},
		{name: "in progress", status: "in_progress", want: ImagePending},
		{name: "pending", status: "pending", want: ImagePending},
		{name: "requested", status: "requested", want: ImagePending},
		{name: "waiting", status: "waiting", want: ImagePending},
		{name: "failure", status: "completed", conclusion: "failure", want: ImageFailed},
		{name: "cancelled", status: "completed", conclusion: "cancelled", want: ImageFailed},
		{name: "timed out", status: "completed", conclusion: "timed_out", want: ImageFailed},
		{name: "neutral", status: "completed", conclusion: "neutral", want: ImageFailed},
		{name: "skipped", status: "completed", conclusion: "skipped", want: ImageFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, api := imageFixture()
			request.BuildStatus, request.BuildConclusion, request.VersionDigest = tc.status, tc.conclusion, ""
			outcome, err := ObserveAndPromoteImage(context.Background(), api, request)
			if err != nil || outcome.Outcome != tc.want || outcome.VersionDigest != "" || api.promotes != 0 {
				t.Fatalf("outcome=%#v err=%v promotes=%d", outcome, err, api.promotes)
			}
		})
	}
}

func TestImagePromotionUnknownOrContradictoryBuildStateFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, status, conclusion, digest string
	}{
		{name: "unknown status", status: "mystery"},
		{name: "completed without conclusion", status: "completed"},
		{name: "pending with conclusion", status: "queued", conclusion: "success"},
		{name: "success without digest", status: "completed", conclusion: "success"},
		{name: "failure with invented digest", status: "completed", conclusion: "failure", digest: "sha256:" + strings.Repeat("a", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, api := imageFixture()
			request.BuildStatus, request.BuildConclusion, request.VersionDigest = tc.status, tc.conclusion, tc.digest
			if _, err := ObserveAndPromoteImage(context.Background(), api, request); err == nil || api.promotes != 0 {
				t.Fatalf("error=%v promotes=%d", err, api.promotes)
			}
		})
	}
}

func TestRecordImageOutcomeValidatesDeduplicatesAndPreservesChain(t *testing.T) {
	request, api := imageFixture()
	request.BuildStatus, request.BuildConclusion, request.VersionDigest = "completed", "failure", ""
	evidence, err := ObserveAndPromoteImage(context.Background(), api, request)
	if err != nil {
		t.Fatal(err)
	}
	record := Record{Reservation: baseReservation()}
	first, err := RecordImageOutcome(record, evidence)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RecordImageOutcome(first, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 1 || len(second.Events) != 1 || VerifyEventChain(record.Reservation.RequestKey, second.Events) != nil {
		t.Fatalf("first=%#v second=%#v", first.Events, second.Events)
	}
	cancelled := evidence
	cancelled.BuildConclusion = "cancelled"
	third, err := RecordImageOutcome(second, cancelled)
	if err != nil {
		t.Fatal(err)
	}
	if len(third.Events) != 2 || VerifyEventChain(record.Reservation.RequestKey, third.Events) != nil || third.Events[0].Digest != first.Events[0].Digest {
		t.Fatalf("events=%#v", third.Events)
	}

	for name, mutate := range map[string]func(*ImagePromotionEvidence){
		"schema":     func(value *ImagePromotionEvidence) { value.SchemaVersion = "2" },
		"repository": func(value *ImagePromotionEvidence) { value.RepositoryFullName = "example/wrong" },
		"package":    func(value *ImagePromotionEvidence) { value.Image = "ghcr.io/example/wrong" },
		"contradictory pending": func(value *ImagePromotionEvidence) {
			value.Outcome, value.BuildStatus, value.BuildConclusion = ImagePending, "completed", "success"
		},
	} {
		t.Run(name, func(t *testing.T) {
			invalid := evidence
			mutate(&invalid)
			if _, err := RecordImageOutcome(record, invalid); err == nil {
				t.Fatal("malformed evidence accepted")
			}
		})
	}

	successRequest, successAPI := imageFixture()
	success, err := ObserveAndPromoteImage(context.Background(), successAPI, successRequest)
	if err != nil {
		t.Fatal(err)
	}
	success.VersionDigest = ""
	if _, err := RecordImageOutcome(record, success); err == nil {
		t.Fatal("successful outcome without digest accepted")
	}
}

func TestImagePromotionNumericGAOrderingAndMissingNewerImage(t *testing.T) {
	request, api := imageFixture()
	request.Version = "v2.9.99"
	request.ModuleTagRef = "refs/tags/contrib/azure/apim-callout/v2.9.99"
	api.module.Ref = request.ModuleTagRef
	api.roots = []ImageSourceTag{
		{Ref: "refs/tags/v2.9.99", ObjectSHA: strings.Repeat("3", 40), CommitSHA: request.CommitSHA},
		{Ref: "refs/tags/v2.10.0", ObjectSHA: strings.Repeat("4", 40), CommitSHA: strings.Repeat("5", 40)},
	}
	if _, err := ObserveAndPromoteImage(context.Background(), api, request); ErrorCode(err) != "image_version_not_highest_ga" || api.promotes != 0 {
		t.Fatalf("error=%q promotes=%d", ErrorCode(err), api.promotes)
	}
}

func TestImagePromotionCanonicalRunCancellationAndPaginationFailClosed(t *testing.T) {
	request, api := imageFixture()
	api.canonical = "99"
	api.canonicals[request.ModuleTagRef] = "99"
	outcome, err := ObserveAndPromoteImage(context.Background(), api, request)
	if err != nil || outcome.Outcome != ImageVersionOnly || api.promotes != 0 {
		t.Fatalf("outcome=%#v err=%v", outcome, err)
	}
	request, api = imageFixture()
	api.run.Status = "cancelled"
	api.run.Conclusion = "cancelled"
	api.runs[request.RunID] = api.run
	if _, err := ObserveAndPromoteImage(context.Background(), api, request); ErrorCode(err) != "image_run_mismatch" {
		t.Fatalf("error=%q", ErrorCode(err))
	}
	request, api = imageFixture()
	api.pageForever = true
	if _, err := ObserveAndPromoteImage(context.Background(), api, request); ErrorCode(err) != "pagination_exhausted" {
		t.Fatalf("error=%q", ErrorCode(err))
	}
}

func TestImagePromotionExistingLatestRequiresIndependentProvenance(t *testing.T) {
	t.Run("verified older latest can advance", func(t *testing.T) {
		request, api := imageFixture()
		addHistoricalLatest(api)
		outcome, err := ObserveAndPromoteImage(context.Background(), api, request)
		if err != nil || outcome.Outcome != ImagePromoted || api.promotes != 1 {
			t.Fatalf("outcome=%#v err=%v promotes=%d", outcome, err, api.promotes)
		}
	})
	for name, mutate := range map[string]func(*fakeImageAPI){
		"falsely claimed older version": func(api *fakeImageAPI) {
			api.latest.Version = "v2.8.0"
		},
		"wrong module tag object": func(api *fakeImageAPI) {
			tag := api.sourceTags["refs/tags/contrib/azure/apim-callout/v2.9.0"]
			tag.ObjectSHA = strings.Repeat("7", 40)
			api.sourceTags[tag.Ref] = tag
		},
		"wrong root tag commit": func(api *fakeImageAPI) {
			tag := api.sourceTags["refs/tags/v2.9.0"]
			tag.CommitSHA = strings.Repeat("7", 40)
			api.sourceTags[tag.Ref] = tag
		},
		"wrong historical workflow run": func(api *fakeImageAPI) {
			run := api.runs["90"]
			run.HeadSHA = strings.Repeat("7", 40)
			api.runs["90"] = run
		},
		"noncanonical historical workflow run": func(api *fakeImageAPI) {
			api.canonicals["refs/tags/contrib/azure/apim-callout/v2.9.0"] = "89"
		},
	} {
		t.Run(name, func(t *testing.T) {
			request, api := imageFixture()
			addHistoricalLatest(api)
			mutate(api)
			if _, err := ObserveAndPromoteImage(context.Background(), api, request); err == nil || api.promotes != 0 {
				t.Fatalf("error=%v promotes=%d", err, api.promotes)
			}
		})
	}
}

func TestImagePromotionExactDigestReconciliationRequiresIndependentProvenance(t *testing.T) {
	request, api := imageFixture()
	api.hasLatest = true
	api.latest = api.candidate
	tag := api.sourceTags[request.ModuleTagRef]
	tag.ObjectSHA = strings.Repeat("7", 40)
	api.afterFirst[request.ModuleTagRef] = tag
	if _, err := ObserveAndPromoteImage(context.Background(), api, request); ErrorCode(err) != "latest_module_tag_mismatch" || api.promotes != 0 {
		t.Fatalf("error=%q promotes=%d", ErrorCode(err), api.promotes)
	}
}

func TestImagePromotionResponseLossReconcilesExactDigest(t *testing.T) {
	request, api := imageFixture()
	api.promoteErr = errors.New("lost response")
	outcome, err := ObserveAndPromoteImage(context.Background(), api, request)
	if err != nil || outcome.Outcome != ImageReconciled || api.promotes != 1 {
		t.Fatalf("outcome=%#v err=%v promotes=%d", outcome, err, api.promotes)
	}
}

func TestImagePromotionWrongDigestAndReadFailuresFailClosed(t *testing.T) {
	t.Run("wrong candidate digest", func(t *testing.T) {
		request, api := imageFixture()
		api.candidate.Digest = "sha256:" + strings.Repeat("b", 64)
		if _, err := ObserveAndPromoteImage(context.Background(), api, request); ErrorCode(err) != "versioned_image_provenance_mismatch" || api.promotes != 0 {
			t.Fatalf("error=%q promotes=%d", ErrorCode(err), api.promotes)
		}
	})
	t.Run("run API failure", func(t *testing.T) {
		request, api := imageFixture()
		api.runErr = context.DeadlineExceeded
		if _, err := ObserveAndPromoteImage(context.Background(), api, request); ErrorCode(err) != "image_run_read_failed" || api.promotes != 0 {
			t.Fatalf("error=%q promotes=%d", ErrorCode(err), api.promotes)
		}
	})
	t.Run("promotion not applied", func(t *testing.T) {
		request, api := imageFixture()
		api.noApply = true
		if _, err := ObserveAndPromoteImage(context.Background(), api, request); ErrorCode(err) != "latest_image_promotion_unconfirmed" || api.promotes != 1 {
			t.Fatalf("error=%q promotes=%d", ErrorCode(err), api.promotes)
		}
	})
	t.Run("malformed root GA provenance", func(t *testing.T) {
		request, api := imageFixture()
		api.roots[0].ObjectSHA = "bad"
		if _, err := ObserveAndPromoteImage(context.Background(), api, request); ErrorCode(err) != "root_tag_provenance_invalid" || api.promotes != 0 {
			t.Fatalf("error=%q promotes=%d", ErrorCode(err), api.promotes)
		}
	})
}

func TestDockerWorkflowsSeparateVersionBuildAndGuardedLatest(t *testing.T) {
	parentRaw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "docker-images-release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	parent := string(parentRaw)
	if strings.Contains(parent, "set_as_latest") || strings.Contains(parent, "&& 'latest'") {
		t.Fatal("release workflow retains caller-selected or unconditional latest")
	}
	for _, required := range []string{"permissions: {}", "promote_latest: ${{ github.event_name == 'push' && github.run_attempt == 1", "tags: ${{ needs.prepare-tag.outputs."} {
		if !strings.Contains(parent, required) {
			t.Fatalf("release workflow missing %q", required)
		}
	}

	childRaw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "docker-build-and-push.yml"))
	if err != nil {
		t.Fatal(err)
	}
	child := string(childRaw)
	for _, item := range ImagePackagePolicy() {
		pair := item.ModulePrefix + "|" + item.Image
		if !strings.Contains(parent, item.ModulePrefix) || !strings.Contains(parent, item.Image) || !strings.Contains(child, pair) {
			t.Fatalf("workflow mapping drift for %#v", item)
		}
	}
	parts := strings.Split(child, "  promote-latest:")
	if len(parts) != 2 {
		t.Fatal("missing one separate latest promotion job")
	}
	if strings.Contains(parts[0], "$IMAGE:latest") {
		t.Fatal("versioned build section writes latest")
	}
	if !strings.Contains(parts[0], `test "$tag" != latest`) {
		t.Fatal("generic versioned builder does not reject latest")
	}
	for _, required := range []string{ImageLatestConcurrencyGroup, "cancel-in-progress: false", "Revalidate source, run, version and immutable image provenance", "$IMAGE@$VERSION_DIGEST", "io.datadog.dd-trace-go.module-tag-object", "test \"$HIGHEST\" = \"$VERSION\"", "test \"$RUN_ATTEMPT\" = 1"} {
		if !strings.Contains(parts[1], required) {
			t.Fatalf("promotion workflow missing %q", required)
		}
	}
}

func TestDockerPromotionEnumeratesCanonicalExactTagRunBeforeLatest(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "docker-build-and-push.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(raw)
	for _, required := range []string{
		`actions/workflows/docker-images-release.yml/runs`,
		`--data-urlencode "event=push"`,
		`--data-urlencode "branch=$REF_NAME"`,
		`--data-urlencode 'per_page=100'`,
		`--max-time 60 --max-filesize 16777216`,
		`while [ "$PAGE" -le 200 ]`,
		`test "$PAGE" -lt 200`,
		`test "$PAGE_COUNT" -lt 100`,
		`.repository.full_name == $repo`,
		`.head_branch == $branch`,
		`.head_sha == $sha`,
		`.run_attempt == 1`,
		`.conclusion == "cancelled"`,
		`else error("malformed image run state")`,
		`CANONICAL_RUN_ID=$(sort -n /tmp/eligible-image-runs | head -1)`,
		`test "$CANONICAL_RUN_ID" = "$RUN_ID"`,
		`/actions/runs/$RUN_ID" > /tmp/run-recheck.json`,
		`CURRENT_MODULE_REF="refs/tags/$CURRENT_MODULE_TAG"`,
		`test "$(git rev-parse "$CURRENT_MODULE_REF")" = "$current_tag_object"`,
		`test "$(git rev-parse "$CURRENT_ROOT_REF^{}")" = "$current_commit"`,
		`/actions/runs/$current_run_id" > /tmp/latest-run.json`,
		`.status == "completed" and .conclusion == "success"`,
		`while [ "$LATEST_PAGE" -le 200 ]`,
		`--data-urlencode "branch=$CURRENT_MODULE_TAG"`,
		`test "$LATEST_PAGE" -lt 200`,
		`test "$(sort -n /tmp/latest-eligible-runs | head -1)" = "$current_run_id"`,
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("canonical production guard missing %q", required)
		}
	}
	latestCall := `docker buildx imagetools create --tag "$IMAGE:latest"`
	if strings.Count(workflow, latestCall) != 1 {
		t.Fatalf("latest mutation callsites=%d, want one", strings.Count(workflow, latestCall))
	}
	canonicalCheck := strings.Index(workflow, `test "$CANONICAL_RUN_ID" = "$RUN_ID"`)
	runRecheck := strings.Index(workflow, `/actions/runs/$RUN_ID" > /tmp/run-recheck.json`)
	historicalCheck := strings.Index(workflow, `test "$(sort -n /tmp/latest-eligible-runs | head -1)" = "$current_run_id"`)
	latestIndex := strings.Index(workflow, latestCall)
	if canonicalCheck < 0 || runRecheck <= canonicalCheck || historicalCheck <= runRecheck || latestIndex <= historicalCheck {
		t.Fatalf("canonical/recheck/historical/latest ordering invalid: canonical=%d recheck=%d historical=%d latest=%d", canonicalCheck, runRecheck, historicalCheck, latestIndex)
	}
}

func TestImagePackagePolicyHasFourExactMappings(t *testing.T) {
	policy := ImagePackagePolicy()
	if len(policy) != 4 {
		t.Fatalf("packages=%d", len(policy))
	}
	seen := map[string]bool{}
	for _, item := range policy {
		if seen[item.Image] || !strings.HasPrefix(item.ModulePrefix, "contrib/") || !strings.HasPrefix(item.Image, "ghcr.io/datadog/dd-trace-go/") {
			t.Fatalf("bad mapping %#v", item)
		}
		seen[item.Image] = true
	}
}
