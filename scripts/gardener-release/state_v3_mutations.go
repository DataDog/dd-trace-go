// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"time"
)

func stateV3MutationArm(kind, ref, expectedOldOID, intendedOID string, intendedKnown bool, intent any) (StateV3MutationArm, bool) {
	digest, ok := stateV3CanonicalDigest(intent)
	if !ok {
		return StateV3MutationArm{}, false
	}
	arm := StateV3MutationArm{OperationKind: kind, Ref: ref, ExpectedOldMissing: expectedOldOID == "", ExpectedOldOID: expectedOldOID, IntendedObjectKnown: intendedKnown, IntendedObjectOID: intendedOID, IntentSHA256: digest, Attempt: 1}
	if !validStateV3MutationRef(ref) || expectedOldOID != "" && !validStateV3OID(expectedOldOID) || intendedKnown != (intendedOID != "") || intendedOID != "" && !validStateV3OID(intendedOID) {
		return StateV3MutationArm{}, false
	}
	return arm, true
}

func validStateV3MutationRef(ref string) bool {
	return strings.HasPrefix(ref, "refs/heads/") && validBranchName(strings.TrimPrefix(ref, "refs/heads/")) || validFullTagRef(ref)
}

func validStateV3BranchEvidence(evidence []StateV3BranchEvidence, record StateV3Record) bool {
	if record.Prepared == nil || len(evidence) > len(record.Prepared.Mutation.Branches) {
		return false
	}
	for index, observed := range evidence {
		intent := record.Prepared.Mutation.Branches[index]
		objectOID := record.Reservation.SourceOID
		response := observed.Response
		if intent.Target == "platform_commit" {
			if record.Commit == nil {
				return false
			}
			objectOID = record.Commit.OID
			response = record.Commit.MutationResponse
			if observed.Response != response {
				return false
			}
		}
		if observed.Intent != intent || observed.ObjectOID != objectOID || observed.ObservedRefOID != objectOID || observed.Status != "present" || !validStateV3MutationResponse(response, objectOID) || observed.Disposition != stateV3MutationDisposition(response, "published") {
			return false
		}
	}
	return true
}

func validStateV3MutationResponse(response StateV3MutationResponse, expectedOID string) bool {
	switch response.Observation {
	case "observed":
		return response.Attempts == 1 && response.OID == expectedOID
	case "lost":
		return response.Attempts == 1 && response.OID == ""
	case "not_attempted": // Exact preread reconciliation; no mutation was issued.
		return response.Attempts == 0 && response.OID == ""
	default:
		return false
	}
}

func stateV3MutationDisposition(response StateV3MutationResponse, published string) string {
	if response.Observation == "not_attempted" {
		return "reconciled"
	}
	return published
}

func validStateV3TagPlans(plans []StateV3TagPlan, mutation StateV3CommitMutationIntent, reservation StateV3Reservation, policy StateV3Policy) bool {
	overhead := StateV3OtherHistoryOverhead
	if len(mutation.Branches) == 3 {
		overhead = StateV3PrepareHistoryOverhead
	}
	lane, laneOK := stateV3LaneForReservation(reservation, policy)
	if !laneOK || len(plans) == 0 || len(plans) > MaxStateV3Tags || 4*len(plans)+overhead > lane.MaxHistoryCommits {
		return false
	}
	seenNames, seenRefs := map[string]bool{}, map[string]bool{}
	rootTags := 0
	previousRef := ""
	version := strings.TrimPrefix(mutation.Message, "release: ")
	for _, plan := range plans {
		if plan.Name == "" || plan.Ref != "refs/tags/"+plan.Name || !validFullTagRef(plan.Ref) || plan.Message != plan.Name+"\n" || plan.TargetRef != mutation.TargetRef || plan.Signature != "absent" || plan.Tagger != policy.Tagger || !validStateV3TaggerDate(plan.TaggerDate) || strings.Contains(plan.Message, "BEGIN PGP SIGNATURE") || seenNames[plan.Name] || seenRefs[plan.Ref] || (previousRef != "" && plan.Ref <= previousRef) {
			return false
		}
		seenNames[plan.Name], seenRefs[plan.Ref] = true, true
		previousRef = plan.Ref
		if plan.Name == version {
			rootTags++
		} else if !strings.HasSuffix(plan.Name, "/"+version) {
			return false
		}
	}
	return rootTags == 1
}

func validStateV3TagIntents(intents []StateV3TagIntent, plans []StateV3TagPlan, commit StateV3AdoptedCommit) bool {
	if len(intents) != len(plans) {
		return false
	}
	seenObjects := map[string]bool{}
	for index, intent := range intents {
		plan := plans[index]
		expectedOID, ok := stateV3TagObjectOID(plan, commit.OID)
		if !ok || intent.Name != plan.Name || intent.Ref != plan.Ref || intent.Message != plan.Message || intent.ObjectType != "tag" || intent.TargetType != "commit" || intent.TargetOID != commit.OID || intent.ExpectedTagObjectOID != expectedOID || intent.ExpectedTagObjectOID == commit.OID || intent.Tagger != plan.Tagger || intent.TaggerDate != plan.TaggerDate || intent.Signature != plan.Signature || seenObjects[intent.ExpectedTagObjectOID] {
			return false
		}
		seenObjects[intent.ExpectedTagObjectOID] = true
	}
	return true
}

func validStateV3TagObjectEvidence(evidence []StateV3TagObjectEvidence, intents []StateV3TagIntent, commit StateV3AdoptedCommit) bool {
	if len(evidence) > len(intents) {
		return false
	}
	for index, observed := range evidence {
		intent := intents[index]
		if observed.Intent != intent || !validStateV3MutationResponse(observed.Response, intent.ExpectedTagObjectOID) || observed.RESTTagObjectOID != intent.ExpectedTagObjectOID || observed.PeeledCommitOID != commit.OID {
			return false
		}
	}
	return true
}

func validStateV3TagEvidence(evidence []StateV3TagEvidence, objects []StateV3TagObjectEvidence, commit StateV3AdoptedCommit) bool {
	if len(evidence) > len(objects) {
		return false
	}
	seenRefs, seenObjects := map[string]bool{}, map[string]bool{}
	for index, observed := range evidence {
		intent := objects[index].Intent
		if observed.Ref != intent.Ref || observed.Name != intent.Name || observed.TagObjectOID != intent.ExpectedTagObjectOID || observed.TagObjectOID == commit.OID || !validStateV3MutationResponse(observed.RefResponse, observed.TagObjectOID) || observed.RefResponse.Observation == "not_attempted" && objects[index].Response.Observation != "not_attempted" || observed.TagObjectOID != observed.ObservedRefOID || observed.TagObjectOID != observed.RESTTagObjectOID || observed.Message != intent.Message || observed.ObjectType != intent.ObjectType || observed.TargetType != intent.TargetType || observed.TargetOID != intent.TargetOID || observed.PeeledCommitOID != commit.OID || observed.Signature != intent.Signature || observed.Tagger != intent.Tagger || observed.TaggerDate != intent.TaggerDate || seenRefs[observed.Ref] || seenObjects[observed.TagObjectOID] {
			return false
		}
		seenRefs[observed.Ref], seenObjects[observed.TagObjectOID] = true, true
	}
	return true
}

func validStateV3TaggerDate(value string) bool {
	parsed, err := time.Parse(time.RFC3339, value)
	return err == nil && parsed.Location() == time.UTC && parsed.Format(time.RFC3339) == value
}

func stateV3TagObjectOID(plan StateV3TagPlan, targetOID string) (string, bool) {
	if !validStateV3OID(targetOID) || !validStateV3TaggerDate(plan.TaggerDate) || !validStateV3RawIdentity(plan.Tagger) {
		return "", false
	}
	date, err := time.Parse(time.RFC3339, plan.TaggerDate)
	if err != nil {
		return "", false
	}
	body := fmt.Sprintf("object %s\ntype commit\ntag %s\ntagger %s <%s> %d +0000\n\n%s", targetOID, plan.Name, plan.Tagger.Name, plan.Tagger.Email, date.Unix(), plan.Message)
	hasher := sha1.New() //nolint:gosec // This computes a Git SHA-1 object ID.
	_, _ = fmt.Fprintf(hasher, "tag %d%c%s", len(body), byte(0), body)
	return hex.EncodeToString(hasher.Sum(nil)), true
}

func validStateV3TestEvidence(evidence StateV3TestEvidence, record StateV3Record, policy StateV3Policy) bool {
	if record.Commit == nil || evidence.SchemaVersion != "1" || evidence.Repository != policy.RepositoryFullName || evidence.WorkflowPath != policy.Test.WorkflowPath || evidence.WorkflowID != policy.Test.WorkflowID || evidence.WorkflowSHA256 != policy.Test.WorkflowSHA256 || evidence.Event != policy.Test.Event || evidence.ReleaseOID != record.Commit.OID || !reflect.DeepEqual(evidence.RequiredJobs, policy.Test.RequiredJobs) {
		return false
	}
	expected := []StateV3TestRunEvidence{{TargetBranch: strings.TrimPrefix(record.Commit.Ref, "refs/heads/"), TargetOID: record.Commit.OID}}
	if record.Reservation.Command == "release:prepare" {
		expected = []StateV3TestRunEvidence{
			{TargetBranch: strings.TrimPrefix(record.Prepared.Mutation.Branches[0].Ref, "refs/heads/"), TargetOID: record.Reservation.SourceOID},
			{TargetBranch: strings.TrimPrefix(record.Commit.Ref, "refs/heads/"), TargetOID: record.Commit.OID},
		}
	}
	if len(evidence.Runs) != len(expected) {
		return false
	}
	seenRuns, seenJobs := map[string]bool{}, map[string]bool{}
	for index, run := range evidence.Runs {
		if run.TargetBranch != expected[index].TargetBranch || run.TargetOID != expected[index].TargetOID || !validBranchName(run.TargetBranch) || !validStateV3OID(run.TargetOID) || !validID(run.RunID) || run.Attempt <= 0 || run.Status != "completed" || run.Conclusion != "success" || seenRuns[run.RunID] || len(run.Jobs) != len(policy.Test.RequiredJobs) {
			return false
		}
		seenRuns[run.RunID] = true
		for jobIndex, job := range run.Jobs {
			if job.Name != policy.Test.RequiredJobs[jobIndex] || !validID(job.ID) || job.Attempt != run.Attempt || job.Status != "completed" || job.Conclusion != "success" || seenJobs[job.ID] {
				return false
			}
			seenJobs[job.ID] = true
		}
	}
	return true
}

func validStateV3PreparePRIntent(intent StateV3PreparePRIntent, record StateV3Record) bool {
	if record.Commit == nil || record.Reservation.Command != "release:prepare" {
		return false
	}
	return intent.Repository == record.Reservation.RepositoryFullName && intent.Base == "main" && intent.Head == strings.TrimPrefix(record.Commit.Ref, "refs/heads/") && intent.HeadOID == record.Commit.OID && validStateV3Text(intent.Title, 256) && intent.BodyMarker == preparePRMarker(record.Reservation.RequestKey) && intent.ExpectedAbsent
}

func validStateV3PreparePREvidence(evidence StateV3PreparePREvidence, record StateV3Record) bool {
	if record.PreparePRIntent == nil || evidence.Intent != *record.PreparePRIntent || evidence.SchemaVersion != "1" || !validStateV3MutationResponse(evidence.Response, evidence.Intent.HeadOID) {
		return false
	}
	return validID(evidence.Number) && evidence.URL == "https://github.com/"+evidence.Intent.Repository+"/pull/"+evidence.Number && evidence.ObservedHead == evidence.Intent.Head && evidence.ObservedOID == evidence.Intent.HeadOID && evidence.ObservedBase == evidence.Intent.Base && evidence.ObservedMarker == evidence.Intent.BodyMarker && evidence.Status == "open" && evidence.Disposition == stateV3MutationDisposition(evidence.Response, "created")
}

func validStateV3OutcomeEvidence(evidence StateV3OutcomeEvidence, record StateV3Record, policy StateV3Policy) bool {
	priorDigest, ok := stateV3OutcomePriorStateDigest(record)
	if !ok || record.Commit == nil || evidence.SchemaVersion != "1" || evidence.RequestKey != record.Reservation.RequestKey || evidence.Command != record.Reservation.Command || evidence.ReleaseOID != record.Commit.OID || evidence.PriorStateSHA256 != priorDigest || evidence.Publication != WorkSucceeded {
		return false
	}
	switch record.Reservation.Command {
	case "release:prepare":
		return evidence.PreparePR == WorkSucceeded && evidence.Images == WorkNotApplicable && len(evidence.ImageEvidence) == 0 && record.PreparePREvidence != nil
	case "release:promote":
		return evidence.PreparePR == WorkNotApplicable && evidence.Images == WorkNotApplicable && len(evidence.ImageEvidence) == 0
	case "release:release":
		if evidence.PreparePR != WorkNotApplicable || evidence.Images != WorkSucceeded || len(evidence.ImageEvidence) != len(imagePackages) {
			return false
		}
		seen := map[string]bool{}
		for _, wrapped := range evidence.ImageEvidence {
			image := wrapped.Detail
			if wrapped.WorkflowID != policy.Image.WorkflowID || validateImagePromotionEvidence(image) != nil || image.RepositoryFullName != policy.RepositoryFullName || image.WorkflowPath != policy.Image.WorkflowPath || image.WorkflowSHA256 != policy.Image.WorkflowSHA256 || image.ChildWorkflowSHA256 != policy.Image.ChildWorkflowSHA256 || image.CommitSHA != record.Commit.OID || !validStateV3OID(image.CommitSHA) || !validStateV3OID(image.ModuleTagObjectSHA) || image.RootTagObjectSHA != "" && !validStateV3OID(image.RootTagObjectSHA) || image.Version != record.Reservation.ResolvedVersion || image.Outcome != ImagePromoted && image.Outcome != ImageReconciled || seen[image.Image] {
				return false
			}
			item, _, err := imagePackageForRequest(ImagePromotionRequest{RepositoryFullName: image.RepositoryFullName, Event: image.Event, WorkflowPath: image.WorkflowPath, RunID: image.RunID, RunAttempt: image.RunAttempt, ModuleTagRef: image.ModuleTagRef, ModuleTagObjectSHA: image.ModuleTagObjectSHA, CommitSHA: image.CommitSHA, Image: image.Image, Version: image.Version, VersionDigest: image.VersionDigest, BuildStatus: image.BuildStatus, BuildConclusion: image.BuildConclusion})
			if err != nil || image.ModuleTagRef != "refs/tags/"+item.ModulePrefix+record.Reservation.ResolvedVersion || !matchesStateV3PublishedTag(record.TagEvidence, image.ModuleTagRef, image.ModuleTagObjectSHA, image.CommitSHA) || !matchesStateV3PublishedTag(record.TagEvidence, "refs/tags/"+record.Reservation.ResolvedVersion, image.RootTagObjectSHA, image.CommitSHA) {
				return false
			}
			seen[image.Image] = true
		}
		return len(seen) == len(imagePackages)
	default:
		return false
	}
}

func stateV3OutcomePriorStateDigest(record StateV3Record) (string, bool) {
	prior := struct {
		Reservation     StateV3Reservation         `json:"reservation"`
		Binding         StateV3ExecutionBinding    `json:"binding"`
		Prepared        *StateV3PreparedState      `json:"prepared"`
		Commit          *StateV3AdoptedCommit      `json:"commit"`
		Arms            []StateV3MutationArm       `json:"arms"`
		Branches        []StateV3BranchEvidence    `json:"branches"`
		TagPlans        []StateV3TagPlan           `json:"tag_plans"`
		TagIntents      []StateV3TagIntent         `json:"tag_intents"`
		TagObjects      []StateV3TagObjectEvidence `json:"tag_objects"`
		Tags            []StateV3TagEvidence       `json:"tags"`
		Tests           *StateV3TestEvidence       `json:"tests"`
		PreparePRIntent *StateV3PreparePRIntent    `json:"prepare_pr_intent,omitempty"`
		PreparePR       *StateV3PreparePREvidence  `json:"prepare_pr,omitempty"`
	}{record.Reservation, record.Binding, record.Prepared, record.Commit, record.MutationArms, record.BranchEvidence, record.TagPlans, record.TagIntents, record.TagObjectEvidence, record.TagEvidence, record.TestEvidence, record.PreparePRIntent, record.PreparePREvidence}
	raw, err := canonicalJSON(prior)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), true
}

func matchesStateV3PublishedTag(tags []StateV3TagEvidence, ref, objectOID, commitOID string) bool {
	for _, tag := range tags {
		if tag.Ref == ref && tag.TagObjectOID == objectOID && tag.PeeledCommitOID == commitOID {
			return true
		}
	}
	return false
}
