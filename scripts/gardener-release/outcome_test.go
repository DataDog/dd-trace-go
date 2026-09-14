// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"strings"
	"testing"
)

func releaseOutcomeFixture() (Record, OperationOutcome) {
	record := checksRecord("release:release")
	record.Phase = PhaseTagsPublished
	record.Reservation.ResolvedVersion = "v2.9.0"
	outcome := OperationOutcome{SchemaVersion: "1", Publication: PublicationOutcome{Status: WorkSucceeded, ReleaseSHA: record.SignedOutput.ReleaseSHA}, PreparePR: PreparePROutcome{Status: WorkNotApplicable}, Images: WorkSucceeded}
	for i, item := range ImagePackagePolicy() {
		outcome.ImageEvidence = append(outcome.ImageEvidence, ImagePromotionEvidence{SchemaVersion: "1", Outcome: ImagePromoted, RepositoryFullName: RepositoryFullName, WorkflowPath: ImageWorkflowPath, RunID: string(rune('1' + i)), RunAttempt: 1, Event: "push", BuildStatus: "completed", BuildConclusion: "success", ModuleTagRef: "refs/tags/" + item.ModulePrefix + "v2.9.0", ModuleTagObjectSHA: strings.Repeat(string(rune('a'+i)), 40), CommitSHA: record.SignedOutput.ReleaseSHA, Version: "v2.9.0", Image: item.Image, VersionDigest: "sha256:" + strings.Repeat(string(rune('a'+i)), 64), RootTagObjectSHA: strings.Repeat("f", 40)})
	}
	return record, outcome
}

func TestRecordOperationOutcomeCompletesOnlyFourGuardedImages(t *testing.T) {
	record, outcome := releaseOutcomeFixture()
	updated, err := RecordOperationOutcome(record, outcome)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Phase != PhaseComplete || len(updated.Events) != 2 || updated.Events[0].Kind != EventOutcomeRecorded || updated.Events[1].Kind != EventPhaseAdvanced {
		t.Fatalf("record=%#v", updated)
	}
	repeated, err := RecordOperationOutcome(updated, outcome)
	if err != nil || len(repeated.Events) != len(updated.Events) {
		t.Fatalf("repeat=%#v err=%v", repeated, err)
	}
}

func TestRecordOperationOutcomePendingFailedStayTagsPublished(t *testing.T) {
	for _, state := range []struct {
		outcome                 ImageOutcome
		status                  WorkOutcome
		buildStatus, conclusion string
	}{{ImagePending, WorkPending, "in_progress", ""}, {ImageFailed, WorkFailed, "completed", "failure"}} {
		record, outcome := releaseOutcomeFixture()
		outcome.Images = state.status
		outcome.ImageEvidence[0].Outcome = state.outcome
		outcome.ImageEvidence[0].BuildStatus = state.buildStatus
		outcome.ImageEvidence[0].BuildConclusion = state.conclusion
		outcome.ImageEvidence[0].VersionDigest = ""
		outcome.ImageEvidence[0].RootTagObjectSHA = ""
		updated, err := RecordOperationOutcome(record, outcome)
		if err != nil {
			t.Fatal(err)
		}
		if updated.Phase != PhaseTagsPublished {
			t.Fatalf("phase=%s", updated.Phase)
		}
	}
}

func TestRecordOperationOutcomeRejectsMissingDuplicateAndVersionedOnlyImages(t *testing.T) {
	for name, mutate := range map[string]func(*OperationOutcome){
		"missing":   func(o *OperationOutcome) { o.ImageEvidence = o.ImageEvidence[:3] },
		"duplicate": func(o *OperationOutcome) { o.ImageEvidence[3] = o.ImageEvidence[0] },
		"wrong sha": func(o *OperationOutcome) { o.ImageEvidence[0].CommitSHA = strings.Repeat("9", 40) },
		"versioned only": func(o *OperationOutcome) {
			o.ImageEvidence[0].Outcome = ImageVersionOnly
			o.ImageEvidence[0].RootTagObjectSHA = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			record, outcome := releaseOutcomeFixture()
			mutate(&outcome)
			if _, err := RecordOperationOutcome(record, outcome); err == nil {
				t.Fatal("hostile outcome accepted")
			}
		})
	}
}
