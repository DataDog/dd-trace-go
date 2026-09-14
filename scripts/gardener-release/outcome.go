// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"encoding/json"
	"sort"
)

// WorkOutcome is the finite post-publication outcome vocabulary persisted
// before public feedback is attempted.
type WorkOutcome string

const (
	WorkNotApplicable WorkOutcome = "not_applicable"
	WorkPending       WorkOutcome = "pending"
	WorkFailed        WorkOutcome = "failed"
	WorkSucceeded     WorkOutcome = "succeeded"
)

type PublicationOutcome struct {
	Status     WorkOutcome `json:"status"`
	ReleaseSHA string      `json:"release_sha"`
}

type PreparePROutcome struct {
	Status WorkOutcome `json:"status"`
	Number string      `json:"number,omitempty"`
}

// OperationOutcome is append-only typed evidence. Image evidence remains the
// exact B11 shape so no free-form status or error can enter durable state.
type OperationOutcome struct {
	SchemaVersion string                   `json:"schema_version"`
	Publication   PublicationOutcome       `json:"publication"`
	PreparePR     PreparePROutcome         `json:"prepare_pr"`
	Images        WorkOutcome              `json:"images"`
	ImageEvidence []ImagePromotionEvidence `json:"image_evidence"`
}

// RecordOperationOutcome validates post-tag results, appends only new evidence,
// and advances to complete only when the command-specific contract is proven.
func RecordOperationOutcome(record Record, outcome OperationOutcome) (Record, error) {
	if outcome.ImageEvidence == nil {
		outcome.ImageEvidence = []ImagePromotionEvidence{}
	}
	if err := validateOperationOutcome(record, outcome); err != nil {
		return Record{}, err
	}
	if err := VerifyEventChain(record.Reservation.RequestKey, record.Events); err != nil {
		return Record{}, err
	}
	for _, event := range record.Events {
		if event.Kind != EventOutcomeRecorded {
			continue
		}
		var prior OperationOutcome
		if json.Unmarshal(event.Evidence, &prior) != nil || validateOperationOutcome(record, prior) != nil {
			return Record{}, newReleaseError(ErrorClassStateConflict, "invalid_recorded_outcome")
		}
		if operationOutcomesEqual(prior, outcome) {
			return record, nil
		}
		if outcomeConflicts(prior, outcome) {
			return Record{}, newReleaseError(ErrorClassStateConflict, "outcome_conflict")
		}
	}
	body, err := json.Marshal(outcome)
	if err != nil {
		return Record{}, wrapReleaseError(ErrorClassStateConflict, "outcome_marshal_failed", err)
	}
	updated := record
	updated.Events, err = AppendEvent(record.Events, record.Reservation.RequestKey, EventOutcomeRecorded, body)
	if err != nil {
		return Record{}, err
	}
	if outcomeComplete(record.Reservation.Command, outcome) && updated.Phase == PhaseTagsPublished {
		if err := advancePublicationPhase(&updated, PhaseComplete); err != nil {
			return Record{}, err
		}
	}
	return updated, nil
}

func validateOperationOutcome(record Record, outcome OperationOutcome) error {
	if !phaseAtLeast(record.Phase, PhaseTagsPublished) || record.SignedOutput == nil || outcome.SchemaVersion != "1" || outcome.Publication.Status != WorkSucceeded || outcome.Publication.ReleaseSHA != record.SignedOutput.ReleaseSHA {
		return newReleaseError(ErrorClassStateConflict, "invalid_operation_outcome")
	}
	switch record.Reservation.Command {
	case "release:prepare":
		if outcome.PreparePR.Status != WorkSucceeded || !validID(outcome.PreparePR.Number) || outcome.Images != WorkNotApplicable || len(outcome.ImageEvidence) != 0 || !hasPreparePREvent(record.Events, outcome.PreparePR.Number) {
			return newReleaseError(ErrorClassStateConflict, "invalid_prepare_outcome")
		}
	case "release:promote":
		if outcome.PreparePR.Status != WorkNotApplicable || outcome.PreparePR.Number != "" || outcome.Images != WorkNotApplicable || len(outcome.ImageEvidence) != 0 {
			return newReleaseError(ErrorClassStateConflict, "invalid_promote_outcome")
		}
	case "release:release":
		if outcome.PreparePR.Status != WorkNotApplicable || outcome.PreparePR.Number != "" || (outcome.Images != WorkPending && outcome.Images != WorkFailed && outcome.Images != WorkSucceeded) {
			return newReleaseError(ErrorClassStateConflict, "invalid_release_outcome")
		}
		if err := validateFourImageOutcomes(record, outcome); err != nil {
			return err
		}
	default:
		return newReleaseError(ErrorClassStateConflict, "invalid_operation_outcome")
	}
	return nil
}

func validateFourImageOutcomes(record Record, outcome OperationOutcome) error {
	if len(outcome.ImageEvidence) != len(imagePackages) {
		return newReleaseError(ErrorClassStateConflict, "image_outcome_incomplete")
	}
	want := map[string]ImagePackage{}
	for _, item := range imagePackages {
		want[item.Image] = item
	}
	seen := map[string]bool{}
	hasPending, hasFailed := false, false
	for _, evidence := range outcome.ImageEvidence {
		if validateImagePromotionEvidence(evidence) != nil || evidence.Version != record.Reservation.ResolvedVersion || evidence.CommitSHA != record.SignedOutput.ReleaseSHA || seen[evidence.Image] {
			return newReleaseError(ErrorClassStateConflict, "invalid_image_outcome")
		}
		item, ok := want[evidence.Image]
		if !ok || evidence.ModuleTagRef != "refs/tags/"+item.ModulePrefix+record.Reservation.ResolvedVersion {
			return newReleaseError(ErrorClassStateConflict, "invalid_image_outcome")
		}
		seen[evidence.Image] = true
		switch evidence.Outcome {
		case ImagePending:
			hasPending = true
		case ImageFailed:
			hasFailed = true
		case ImagePromoted, ImageReconciled:
		default:
			return newReleaseError(ErrorClassStateConflict, "image_latest_outcome_required")
		}
	}
	wantStatus := WorkSucceeded
	if hasFailed {
		wantStatus = WorkFailed
	} else if hasPending {
		wantStatus = WorkPending
	}
	if outcome.Images != wantStatus {
		return newReleaseError(ErrorClassStateConflict, "image_outcome_status_mismatch")
	}
	return nil
}

func outcomeComplete(command string, outcome OperationOutcome) bool {
	return command != "release:release" || outcome.Images == WorkSucceeded
}

func outcomeConflicts(prior, next OperationOutcome) bool {
	if prior.Publication != next.Publication || prior.PreparePR != next.PreparePR || prior.SchemaVersion != next.SchemaVersion {
		return true
	}
	old := map[string]ImagePromotionEvidence{}
	for _, item := range prior.ImageEvidence {
		old[item.Image] = item
	}
	for _, item := range next.ImageEvidence {
		before, ok := old[item.Image]
		if !ok {
			continue
		}
		if before.Outcome == ImagePromoted || before.Outcome == ImageReconciled || before.Outcome == ImageFailed {
			if before != item {
				return true
			}
		} else if before.RunID != item.RunID || before.RunAttempt != item.RunAttempt || before.ModuleTagRef != item.ModuleTagRef || before.ModuleTagObjectSHA != item.ModuleTagObjectSHA || before.CommitSHA != item.CommitSHA {
			return true
		}
	}
	return false
}

func operationOutcomesEqual(left, right OperationOutcome) bool {
	left.ImageEvidence = append([]ImagePromotionEvidence(nil), left.ImageEvidence...)
	right.ImageEvidence = append([]ImagePromotionEvidence(nil), right.ImageEvidence...)
	sort.Slice(left.ImageEvidence, func(i, j int) bool { return left.ImageEvidence[i].Image < left.ImageEvidence[j].Image })
	sort.Slice(right.ImageEvidence, func(i, j int) bool { return right.ImageEvidence[i].Image < right.ImageEvidence[j].Image })
	l, _ := canonicalJSON(left)
	r, _ := canonicalJSON(right)
	return string(l) == string(r)
}

func LatestOperationOutcome(record Record) (OperationOutcome, bool) {
	for i := len(record.Events) - 1; i >= 0; i-- {
		if record.Events[i].Kind == EventOutcomeRecorded {
			var outcome OperationOutcome
			if json.Unmarshal(record.Events[i].Evidence, &outcome) == nil && validateOperationOutcome(record, outcome) == nil {
				return outcome, true
			}
			return OperationOutcome{}, false
		}
	}
	return OperationOutcome{}, false
}
