// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// validateLoadedRecord is the single semantic gate between authenticated state
// bytes and every resume decision. It deliberately uses only immutable values
// carried by the record and repository constants: replay never consults mutable
// current policy.
func validateLoadedRecord(record Record) error {
	invalid := func() error { return newReleaseError(ErrorClassStateConflict, "invalid_loaded_record") }
	if validateNewReservation(record.Reservation) != nil || !KnownPhase(record.Phase) || !ValidGitObjectID(record.WorkflowSHA) || !ValidGitObjectID(record.ToolSHA) {
		return invalid()
	}
	if phaseAtLeast(record.Phase, PhaseSigned) != (record.SignedOutput != nil) {
		return invalid()
	}
	if record.SignedOutput != nil && validateLoadedSignedOutput(record.Reservation, *record.SignedOutput) != nil {
		return invalid()
	}
	if err := validateLoadedEventProgression(record); err != nil {
		return err
	}
	return nil
}

func validateLoadedSignedOutput(reservation Reservation, signed SignedOutput) error {
	invalid := func() error { return newReleaseError(ErrorClassStateConflict, "invalid_loaded_signed_output") }
	if validateSignedOutputForPublication(reservation, signed) != nil || !lowerHexDigest(signed.ToolDigest) || !lowerHexDigest(signed.ValidatorDigest) || !lowerHexDigest(signed.SignerFingerprint) {
		return invalid()
	}
	if signed.SourceSHA != reservation.SourceRefs[0].SHA || signed.SourceSHA == signed.UnsignedSHA || signed.TreeSHA == signed.SourceSHA || signed.TreeSHA == signed.UnsignedSHA || signed.TreeSHA == signed.ReleaseSHA {
		return invalid()
	}
	paths, err := StatePaths(reservation.RepositoryID, reservation.OriginalCommentID)
	if err != nil || signed.Bundle.Path != paths.RecoveryBundle || signed.Bundle.SizeBytes <= 0 || signed.Bundle.SizeBytes > MaxRecoveryBundleBytes || !lowerHexDigest(signed.Bundle.SHA256) || len(signed.Bundle.PrerequisiteSHAs) != 1 || signed.Bundle.PrerequisiteSHAs[0] != signed.SourceSHA {
		return invalid()
	}
	if len(signed.ChangedPaths) == 0 || len(signed.ChangedPaths) > 1024 || !sort.StringsAreSorted(signed.ChangedPaths) {
		return invalid()
	}
	seenPaths := map[string]bool{}
	for _, path := range signed.ChangedPaths {
		if !validRepositoryRelativePath(path) || seenPaths[path] {
			return invalid()
		}
		seenPaths[path] = true
	}
	if len(signed.Tags) == 0 || len(signed.Tags) > 1024 {
		return invalid()
	}
	seenNames, seenRefs, seenObjects := map[string]bool{}, map[string]bool{}, map[string]bool{}
	rootTags := 0
	for _, tag := range signed.Tags {
		if !validTagRef(tag) || tag.PeeledCommitSHA != signed.ReleaseSHA || seenNames[tag.Name] || seenRefs[tag.Ref] || seenObjects[tag.TagObjectSHA] || !strings.HasSuffix(tag.Name, reservation.GenerationVersion) {
			return invalid()
		}
		seenNames[tag.Name], seenRefs[tag.Ref], seenObjects[tag.TagObjectSHA] = true, true, true
		if tag.Name == reservation.GenerationVersion {
			rootTags++
		}
	}
	if rootTags != 1 || attestationResolvedVersion(signed) != reservation.GenerationVersion {
		return invalid()
	}
	return nil
}

func validateLoadedEventProgression(record Record) error {
	invalid := func() error { return newReleaseError(ErrorClassStateConflict, "event_state_mismatch") }
	if len(record.Events) < 2 || record.Events[0].Kind != EventReserved {
		return invalid()
	}
	observedPhase := PhaseReserved
	signingIntents := 0
	signingFingerprint := ""
	branchEvidence := map[string]refEventEvidence{}
	tagEvidence := map[string]refEventEvidence{}
	testEvents := 0
	prEvents := 0
	imageEvidence := map[string]ImagePromotionEvidence{}
	var outcomes []OperationOutcome

	for index, event := range record.Events {
		switch event.Kind {
		case EventReserved:
			if index != 0 {
				return invalid()
			}
			var evidence reservedEventEvidence
			if decodeStrictStateJSON(event.Evidence, &evidence) != nil || evidence.ResolvedVersion != record.Reservation.ResolvedVersion || evidence.ReleaseLine != record.Reservation.ReleaseLine {
				return invalid()
			}
		case EventSigningIntent:
			if observedPhase != PhaseReserved || signingIntents != 0 {
				return invalid()
			}
			var evidence GitSigningIntent
			var ok bool
			if decodeStrictStateJSON(event.Evidence, &evidence) != nil || evidence.Timestamp <= 0 || evidence.Message != "release: "+record.Reservation.GenerationVersion || !validSigningPrincipal(evidence.Principal) {
				return invalid()
			}
			signingFingerprint, ok = openSSHHexFingerprint(evidence.Fingerprint)
			if !ok {
				return invalid()
			}
			signingIntents++
		case EventPhaseAdvanced:
			var evidence phaseEventEvidence
			if decodeStrictStateJSON(event.Evidence, &evidence) != nil || phaseOrder[evidence.Phase] != phaseOrder[observedPhase]+1 {
				return invalid()
			}
			switch evidence.Phase {
			case PhaseSigned:
				if signingIntents != 1 || record.SignedOutput == nil || signingFingerprint != record.SignedOutput.SignerFingerprint || evidence.ReleaseSHA != record.SignedOutput.ReleaseSHA || !lowerHexDigest(evidence.GenerationArtifactSHA256) || !validID(evidence.WorkflowRunID) || evidence.WorkflowRunAttempt <= 0 {
					return invalid()
				}
			case PhaseBranchesPublished:
				if !allPublicationIntentsRecorded(record, branchEvidence) {
					return invalid()
				}
			case PhaseTestsPassed:
				if testEvents != 1 {
					return invalid()
				}
			case PhaseTagsPublished:
				if !allTagsRecorded(record, tagEvidence) {
					return invalid()
				}
			case PhaseComplete:
				if len(outcomes) == 0 || !outcomeComplete(record.Reservation.Command, outcomes[len(outcomes)-1]) || index == 0 || record.Events[index-1].Kind != EventOutcomeRecorded {
					return invalid()
				}
			default:
				return invalid()
			}
			observedPhase = evidence.Phase
		case EventBranchPublished:
			if observedPhase != PhaseSigned || record.SignedOutput == nil {
				return invalid()
			}
			var evidence refEventEvidence
			if decodeStrictStateJSON(event.Evidence, &evidence) != nil || !validRefDisposition(evidence.Disposition) || branchEvidence[evidence.Ref].Ref != "" || !matchesPublicationIntent(*record.SignedOutput, evidence) {
				return invalid()
			}
			branchEvidence[evidence.Ref] = evidence
		case EventTestsPassed:
			if observedPhase != PhaseBranchesPublished || testEvents != 0 || validateLoadedTestEvidence(record, event.Evidence) != nil {
				return invalid()
			}
			testEvents++
		case EventTagPublished:
			if observedPhase != PhaseTestsPassed || record.SignedOutput == nil {
				return invalid()
			}
			var evidence refEventEvidence
			if decodeStrictStateJSON(event.Evidence, &evidence) != nil || !validRefDisposition(evidence.Disposition) || tagEvidence[evidence.Ref].Ref != "" || !matchesSignedTag(*record.SignedOutput, evidence) {
				return invalid()
			}
			tagEvidence[evidence.Ref] = evidence
		case EventPreparePRRecorded:
			if observedPhase != PhaseTagsPublished || prEvents != 0 || validateLoadedPreparePREvidence(record, event.Evidence) != nil {
				return invalid()
			}
			prEvents++
		case EventImageObserved:
			if observedPhase != PhaseTagsPublished || record.Reservation.Command != "release:release" {
				return invalid()
			}
			var evidence ImagePromotionEvidence
			if decodeStrictStateJSON(event.Evidence, &evidence) != nil || validateLoadedImageEvidence(record, evidence) != nil {
				return invalid()
			}
			if prior, ok := imageEvidence[evidence.Image]; ok && (prior == evidence || imageEvidenceConflicts(prior, evidence)) {
				return invalid()
			}
			imageEvidence[evidence.Image] = evidence
		case EventOutcomeRecorded:
			if observedPhase != PhaseTagsPublished {
				return invalid()
			}
			var evidence OperationOutcome
			probe := record
			probe.Phase = PhaseTagsPublished
			probe.Events = record.Events[:index]
			if decodeStrictStateJSON(event.Evidence, &evidence) != nil || validateOperationOutcome(probe, evidence) != nil || validateLoadedOutcomeEvidence(record, evidence) != nil {
				return invalid()
			}
			for _, prior := range outcomes {
				if operationOutcomesEqual(prior, evidence) || outcomeConflicts(prior, evidence) {
					return invalid()
				}
			}
			outcomes = append(outcomes, evidence)
		case EventAcknowledgementBound, EventFailureObserved:
			// There is no production state writer for these legacy reserved
			// kinds. Accepting any shape would create state that production
			// cannot generate or safely replay.
			return invalid()
		default:
			return invalid()
		}
	}
	if observedPhase != record.Phase {
		return invalid()
	}
	if phaseAtLeast(record.Phase, PhaseSigned) && signingIntents != 1 || phaseAtLeast(record.Phase, PhaseBranchesPublished) && !allPublicationIntentsRecorded(record, branchEvidence) || phaseAtLeast(record.Phase, PhaseTestsPassed) && testEvents != 1 || phaseAtLeast(record.Phase, PhaseTagsPublished) && !allTagsRecorded(record, tagEvidence) {
		return invalid()
	}
	if record.Phase == PhaseComplete && len(outcomes) == 0 {
		return invalid()
	}
	return nil
}

func validSigningPrincipal(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, " \t\r\n,\x00") && !looksPlaceholder(value)
}

func validOpenSSHFingerprint(value string) bool {
	_, ok := openSSHHexFingerprint(value)
	return ok
}

func openSSHHexFingerprint(value string) (string, bool) {
	if !strings.HasPrefix(value, "SHA256:") || strings.ContainsAny(value, " \t\r\n\x00") || looksPlaceholder(value) {
		return "", false
	}
	digest, err := base64.RawStdEncoding.Strict().DecodeString(strings.TrimPrefix(value, "SHA256:"))
	if err != nil || len(digest) != sha256.Size {
		return "", false
	}
	return hex.EncodeToString(digest), true
}

func validRefDisposition(value string) bool { return value == "published" || value == "reconciled" }

func matchesPublicationIntent(signed SignedOutput, evidence refEventEvidence) bool {
	for _, intent := range signed.PublicationIntents {
		if evidence.Ref == intent.Ref && evidence.ExpectedOldSHA == intent.ExpectedOldSHA && evidence.ObjectSHA == intent.DesiredSHA {
			return true
		}
	}
	return false
}

func matchesSignedTag(signed SignedOutput, evidence refEventEvidence) bool {
	for _, tag := range signed.Tags {
		if evidence.Ref == tag.Ref && evidence.ObjectSHA == tag.TagObjectSHA {
			return true
		}
	}
	return false
}

func allPublicationIntentsRecorded(record Record, evidence map[string]refEventEvidence) bool {
	if record.SignedOutput == nil || len(evidence) != len(record.SignedOutput.PublicationIntents) {
		return false
	}
	for _, intent := range record.SignedOutput.PublicationIntents {
		item, ok := evidence[intent.Ref]
		if !ok || item.ExpectedOldSHA != intent.ExpectedOldSHA || item.ObjectSHA != intent.DesiredSHA || !validRefDisposition(item.Disposition) {
			return false
		}
	}
	return true
}

func allTagsRecorded(record Record, evidence map[string]refEventEvidence) bool {
	if record.SignedOutput == nil || len(evidence) != len(record.SignedOutput.Tags) {
		return false
	}
	for _, tag := range record.SignedOutput.Tags {
		item, ok := evidence[tag.Ref]
		if !ok || item.ExpectedOldSHA != "" || item.ObjectSHA != tag.TagObjectSHA || !validRefDisposition(item.Disposition) {
			return false
		}
	}
	return true
}

func validateLoadedTestEvidence(record Record, raw json.RawMessage) error {
	invalid := func() error { return newReleaseError(ErrorClassStateConflict, "invalid_test_event") }
	var body testEventEvidence
	if decodeStrictStateJSON(raw, &body) != nil || record.SignedOutput == nil || body.Evidence.SchemaVersion != "1" || body.Evidence.ReleaseSHA != record.SignedOutput.ReleaseSHA || !lowerHexDigest(body.Evidence.EvidenceSHA256) {
		return invalid()
	}
	detail := body.Detail
	if detail.SchemaVersion != "1" || detail.Repository != RepositoryFullName || detail.WorkflowPath != MainBranchTestWorkflowPath || !validID(detail.WorkflowID) || !lowerHexDigest(detail.WorkflowSHA) || detail.Event != "push" || detail.ReleaseSHA != record.SignedOutput.ReleaseSHA || len(detail.RequiredJobs) == 0 || len(detail.RequiredJobs) > 100 || !sort.StringsAreSorted(detail.RequiredJobs) {
		return invalid()
	}
	requiredJobs := map[string]bool{}
	for _, name := range detail.RequiredJobs {
		if name == "" || strings.TrimSpace(name) != name || requiredJobs[name] {
			return invalid()
		}
		requiredJobs[name] = true
	}
	canonical, err := canonicalJSON(detail)
	if err != nil {
		return invalid()
	}
	digest := sha256.Sum256(canonical)
	if hex.EncodeToString(digest[:]) != body.Evidence.EvidenceSHA256 {
		return invalid()
	}
	targets, err := requiredTestTargets(record)
	if err != nil || len(detail.Runs) != len(targets) {
		return invalid()
	}
	seenRuns, seenBranches, seenJobs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, run := range detail.Runs {
		matched := false
		for _, target := range targets {
			if run.TargetBranch == target.Branch && run.TargetSHA == target.SHA {
				matched = true
				break
			}
		}
		if !matched || !validBranchName(run.TargetBranch) || !ValidGitObjectID(run.TargetSHA) || !validID(run.RunID) || run.Attempt <= 0 || run.Status != "completed" || run.Conclusion != "success" || len(run.Jobs) == 0 || len(run.Jobs) > 100 || seenRuns[run.RunID] || seenBranches[run.TargetBranch] {
			return invalid()
		}
		seenRuns[run.RunID], seenBranches[run.TargetBranch] = true, true
		seenNames := map[string]bool{}
		for _, job := range run.Jobs {
			if !validID(job.ID) || !requiredJobs[job.Name] || job.Attempt != run.Attempt || job.Status != "completed" || job.Conclusion != "success" || seenJobs[job.ID] || seenNames[job.Name] {
				return invalid()
			}
			seenJobs[job.ID], seenNames[job.Name] = true, true
		}
		if len(seenNames) != len(requiredJobs) {
			return invalid()
		}
	}
	return nil
}

func validateLoadedPreparePREvidence(record Record, raw json.RawMessage) error {
	invalid := func() error { return newReleaseError(ErrorClassStateConflict, "invalid_prepare_pr_event") }
	if record.Reservation.Command != "release:prepare" || record.SignedOutput == nil || len(record.SignedOutput.PublicationIntents) != 2 {
		return invalid()
	}
	var evidence preparePREventEvidence
	if decodeStrictStateJSON(raw, &evidence) != nil {
		return invalid()
	}
	dev := record.SignedOutput.PublicationIntents[1]
	wantMarker := preparePRMarker(record.Reservation.RequestKey)
	if evidence.SchemaVersion != "1" || evidence.Repository != record.Reservation.RepositoryFullName || evidence.Command != record.Reservation.Command || !validID(evidence.Number) || evidence.URL != "https://github.com/DataDog/dd-trace-go/pull/"+evidence.Number || evidence.Head != strings.TrimPrefix(dev.Ref, "refs/heads/") || evidence.SHA != dev.DesiredSHA || evidence.SHA != record.SignedOutput.ReleaseSHA || evidence.Base != "main" || evidence.Marker != wantMarker || evidence.Disposition != "created" && evidence.Disposition != "reconciled" {
		return invalid()
	}
	return nil
}

func validateLoadedImageEvidence(record Record, evidence ImagePromotionEvidence) error {
	if record.SignedOutput == nil || validateImagePromotionEvidence(evidence) != nil || !lowerHexDigest(evidence.WorkflowSHA256) || !lowerHexDigest(evidence.ChildWorkflowSHA256) || evidence.Version != record.Reservation.ResolvedVersion || evidence.CommitSHA != record.SignedOutput.ReleaseSHA {
		return newReleaseError(ErrorClassStateConflict, "invalid_image_event")
	}
	item, _, err := imagePackageForRequest(ImagePromotionRequest{RepositoryFullName: evidence.RepositoryFullName, Event: evidence.Event, WorkflowPath: evidence.WorkflowPath, RunID: evidence.RunID, RunAttempt: evidence.RunAttempt, ModuleTagRef: evidence.ModuleTagRef, ModuleTagObjectSHA: evidence.ModuleTagObjectSHA, CommitSHA: evidence.CommitSHA, Image: evidence.Image, Version: evidence.Version, VersionDigest: evidence.VersionDigest, BuildStatus: evidence.BuildStatus, BuildConclusion: evidence.BuildConclusion})
	if err != nil || evidence.ModuleTagRef != "refs/tags/"+item.ModulePrefix+record.Reservation.ResolvedVersion {
		return newReleaseError(ErrorClassStateConflict, "invalid_image_event")
	}
	moduleFound, rootFound := false, evidence.RootTagObjectSHA == ""
	for _, tag := range record.SignedOutput.Tags {
		if tag.Ref == evidence.ModuleTagRef && tag.TagObjectSHA == evidence.ModuleTagObjectSHA && tag.PeeledCommitSHA == evidence.CommitSHA {
			moduleFound = true
		}
		if tag.Name == record.Reservation.ResolvedVersion && tag.TagObjectSHA == evidence.RootTagObjectSHA {
			rootFound = true
		}
	}
	if !moduleFound || !rootFound {
		return newReleaseError(ErrorClassStateConflict, "invalid_image_event")
	}
	return nil
}

func validateLoadedOutcomeEvidence(record Record, outcome OperationOutcome) error {
	if record.Reservation.Command != "release:release" {
		if len(outcome.ImageEvidence) != 0 {
			return newReleaseError(ErrorClassStateConflict, "invalid_outcome_event")
		}
		return nil
	}
	workflowDigest, childDigest := "", ""
	for _, evidence := range outcome.ImageEvidence {
		if validateLoadedImageEvidence(record, evidence) != nil {
			return newReleaseError(ErrorClassStateConflict, "invalid_outcome_event")
		}
		if workflowDigest == "" {
			workflowDigest, childDigest = evidence.WorkflowSHA256, evidence.ChildWorkflowSHA256
		} else if evidence.WorkflowSHA256 != workflowDigest || evidence.ChildWorkflowSHA256 != childDigest {
			return newReleaseError(ErrorClassStateConflict, "invalid_outcome_event")
		}
	}
	return nil
}

func imageEvidenceConflicts(prior, next ImagePromotionEvidence) bool {
	if prior == next {
		return false
	}
	switch prior.Outcome {
	case ImagePromoted, ImageReconciled, ImageFailed:
		return true
	}
	return prior.RunID != next.RunID || prior.RunAttempt != next.RunAttempt || prior.ModuleTagRef != next.ModuleTagRef || prior.ModuleTagObjectSHA != next.ModuleTagObjectSHA || prior.CommitSHA != next.CommitSHA || prior.Image != next.Image || prior.Version != next.Version
}
