// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
)

func validStateV3Events(record StateV3Record) bool {
	if len(record.Events) == 0 || len(record.Events) > 10_000 || record.Events[0].Kind != StateV3EventReserved || record.Events[0].Phase != StateV3PhaseReserved {
		return false
	}
	observedPhase := StateV3PhaseReserved
	adopted, testsPassed, preparePRArmed, preparePRRecorded, outcome := false, false, false, false, false
	branchesPublished, tagObjects, publishedTags, armsSeen := 0, 0, 0, 0
	var pending *StateV3MutationArm
	previous := ""
	consume := func(response StateV3MutationResponse, kind, ref string) bool {
		if response.Observation == "not_attempted" {
			return pending == nil
		}
		if pending == nil || pending.OperationKind != kind || pending.Ref != ref {
			return false
		}
		pending = nil
		return true
	}
	for index, event := range record.Events {
		if event.Sequence != index+1 || event.PreviousDigest != previous || !lowerHexDigest(event.EvidenceSHA256) || !lowerHexDigest(event.Digest) {
			return false
		}
		expectedEvidence, ok := stateV3ExpectedEventEvidenceDigest(record, event, index)
		if !ok || event.EvidenceSHA256 != expectedEvidence {
			return false
		}
		digest, err := stateV3EventDigest(event)
		if err != nil || digest != event.Digest {
			return false
		}
		previous = event.Digest
		if pending != nil && event.Kind != stateV3MutationResultKind(pending.OperationKind) {
			return false
		}
		switch event.Kind {
		case StateV3EventReserved:
			if index != 0 || event.Phase != StateV3PhaseReserved || !emptyStateV3RefEvent(event) {
				return false
			}
		case StateV3EventMutationArmed:
			if pending != nil || armsSeen >= len(record.MutationArms) || event.Disposition != "armed" {
				return false
			}
			arm := record.MutationArms[armsSeen]
			expected, valid := stateV3ExpectedMutationArm(record, arm.OperationKind, observedPhase, adopted, preparePRRecorded, branchesPublished, tagObjects, publishedTags)
			if !valid || arm != expected || event.Phase != observedPhase || event.Ref != arm.Ref || event.ExpectedOldOID != arm.ExpectedOldOID || event.ObjectOID != arm.IntendedObjectOID {
				return false
			}
			pending = &record.MutationArms[armsSeen]
			if arm.OperationKind == "prepare_pr_create" {
				preparePRArmed = true
			}
			armsSeen++
		case StateV3EventPhaseAdvanced:
			if stateV3PhaseOrder[event.Phase] != stateV3PhaseOrder[observedPhase]+1 || !emptyStateV3RefEvent(event) {
				return false
			}
			switch event.Phase {
			case StateV3PhasePrepared:
				if record.Prepared == nil {
					return false
				}
			case StateV3PhaseBranchesPublished:
				if !adopted || record.Prepared == nil || branchesPublished != len(record.Prepared.Mutation.Branches) {
					return false
				}
			case StateV3PhaseTestsPassed:
				if !testsPassed {
					return false
				}
			case StateV3PhaseTagsPublished:
				if publishedTags != len(record.TagEvidence) || tagObjects != len(record.TagObjectEvidence) || len(record.TagEvidence) != len(record.TagIntents) || len(record.TagObjectEvidence) != len(record.TagIntents) {
					return false
				}
			case StateV3PhaseComplete:
				if !outcome {
					return false
				}
			default:
				return false
			}
			observedPhase = event.Phase
		case StateV3EventPlatformCommitAdopted:
			wantBranches := 0
			if record.Reservation.Command == "release:prepare" {
				wantBranches = 2
			}
			if observedPhase != StateV3PhasePrepared || adopted || branchesPublished != wantBranches || record.Commit == nil || !consume(record.Commit.MutationResponse, "platform_commit", record.Commit.Ref) || event.Phase != observedPhase || event.Ref != record.Commit.Ref || event.ObjectOID != record.Commit.OID || event.ExpectedOldOID != record.Commit.ParentOID || event.Disposition != stateV3MutationDisposition(record.Commit.MutationResponse, "adopted") {
				return false
			}
			adopted = true
		case StateV3EventBranchPublished:
			if observedPhase != StateV3PhasePrepared || record.Prepared == nil || branchesPublished >= len(record.BranchEvidence) {
				return false
			}
			evidence := record.BranchEvidence[branchesPublished]
			if evidence.Intent.Target == "platform_commit" {
				if !adopted || pending != nil || record.Commit == nil || evidence.Response != record.Commit.MutationResponse {
					return false
				}
			} else if adopted || !consume(evidence.Response, "branch_ref_create", evidence.Intent.Ref) {
				return false
			}
			if event.Phase != observedPhase || event.Ref != evidence.Intent.Ref || event.ExpectedOldOID != evidence.Intent.ExpectedOldOID || event.ObjectOID != evidence.ObjectOID || event.Disposition != evidence.Disposition {
				return false
			}
			branchesPublished++
		case StateV3EventTestsPassed:
			if observedPhase != StateV3PhaseBranchesPublished || testsPassed || event.Phase != observedPhase || !emptyStateV3RefEvent(event) {
				return false
			}
			testsPassed = true
		case StateV3EventTagObjectCreated:
			if observedPhase != StateV3PhaseTestsPassed || tagObjects != publishedTags || tagObjects >= len(record.TagObjectEvidence) {
				return false
			}
			evidence := record.TagObjectEvidence[tagObjects]
			if !consume(evidence.Response, "tag_object_create", evidence.Intent.Ref) || event.Phase != observedPhase || event.Ref != evidence.Intent.Ref || event.ExpectedOldOID != "" || event.ObjectOID != evidence.Intent.ExpectedTagObjectOID || event.Disposition != stateV3MutationDisposition(evidence.Response, "created") {
				return false
			}
			tagObjects++
		case StateV3EventTagPublished:
			if observedPhase != StateV3PhaseTestsPassed || tagObjects != publishedTags+1 || publishedTags >= len(record.TagEvidence) {
				return false
			}
			evidence := record.TagEvidence[publishedTags]
			if !consume(evidence.RefResponse, "tag_ref_create", evidence.Ref) || !matchesStateV3TagEvidence(evidence, event) || event.Phase != observedPhase || event.Disposition != stateV3MutationDisposition(evidence.RefResponse, "published") {
				return false
			}
			publishedTags++
		case StateV3EventPreparePRRecorded:
			if observedPhase != StateV3PhaseTagsPublished || record.Reservation.Command != "release:prepare" || preparePRRecorded || record.PreparePRIntent == nil || record.PreparePREvidence == nil || !consume(record.PreparePREvidence.Response, "prepare_pr_create", record.Commit.Ref) || event.Phase != observedPhase || event.Ref != record.Commit.Ref || event.ExpectedOldOID != "" || event.ObjectOID != record.Commit.OID || event.Disposition != record.PreparePREvidence.Disposition {
				return false
			}
			preparePRRecorded = true
		case StateV3EventOutcomeRecorded:
			if observedPhase != StateV3PhaseTagsPublished || outcome || record.OutcomeEvidence == nil || record.Reservation.Command == "release:prepare" && !preparePRRecorded || event.Phase != observedPhase || !emptyStateV3RefEvent(event) {
				return false
			}
			outcome = true
		default:
			return false
		}
	}
	return observedPhase == record.Phase && adopted == (record.Commit != nil) && testsPassed == (record.TestEvidence != nil) && (preparePRArmed || preparePRRecorded) == (record.PreparePRIntent != nil) && preparePRRecorded == (record.PreparePREvidence != nil) && outcome == (record.OutcomeEvidence != nil) && branchesPublished == len(record.BranchEvidence) && tagObjects == len(record.TagObjectEvidence) && publishedTags == len(record.TagEvidence) && armsSeen == len(record.MutationArms)
}

func stateV3MutationResultKind(operation string) StateV3EventKind {
	switch operation {
	case "platform_commit":
		return StateV3EventPlatformCommitAdopted
	case "branch_ref_create":
		return StateV3EventBranchPublished
	case "tag_object_create":
		return StateV3EventTagObjectCreated
	case "tag_ref_create":
		return StateV3EventTagPublished
	case "prepare_pr_create":
		return StateV3EventPreparePRRecorded
	default:
		return ""
	}
}

func stateV3ExpectedMutationArm(record StateV3Record, operation string, phase StateV3Phase, adopted, preparePRRecorded bool, branchIndex, objectIndex, refIndex int) (StateV3MutationArm, bool) {
	if record.Prepared == nil {
		return StateV3MutationArm{}, false
	}
	switch operation {
	case "platform_commit":
		wantBranches := 0
		if record.Reservation.Command == "release:prepare" {
			wantBranches = 2
		}
		if phase != StateV3PhasePrepared || adopted || branchIndex != wantBranches {
			return StateV3MutationArm{}, false
		}
		return stateV3MutationArm(operation, record.Prepared.Mutation.TargetRef, record.Prepared.Mutation.ExpectedHeadOID, "", false, record.Prepared.Mutation)
	case "branch_ref_create":
		if phase != StateV3PhasePrepared || adopted || branchIndex >= len(record.Prepared.Mutation.Branches) || record.Prepared.Mutation.Branches[branchIndex].Target != "source" {
			return StateV3MutationArm{}, false
		}
		intent := record.Prepared.Mutation.Branches[branchIndex]
		return stateV3MutationArm(operation, intent.Ref, intent.ExpectedOldOID, record.Reservation.SourceOID, true, intent)
	case "tag_object_create":
		if phase != StateV3PhaseTestsPassed || objectIndex != refIndex || objectIndex >= len(record.TagIntents) {
			return StateV3MutationArm{}, false
		}
		intent := record.TagIntents[objectIndex]
		return stateV3MutationArm(operation, intent.Ref, "", intent.ExpectedTagObjectOID, true, intent)
	case "tag_ref_create":
		if phase != StateV3PhaseTestsPassed || objectIndex != refIndex+1 || refIndex >= len(record.TagIntents) || refIndex >= len(record.TagObjectEvidence) {
			return StateV3MutationArm{}, false
		}
		intent := record.TagIntents[refIndex]
		return stateV3MutationArm(operation, intent.Ref, "", intent.ExpectedTagObjectOID, true, struct {
			Ref       string `json:"ref"`
			ObjectOID string `json:"object_oid"`
		}{intent.Ref, intent.ExpectedTagObjectOID})
	case "prepare_pr_create":
		if phase != StateV3PhaseTagsPublished || preparePRRecorded || record.Reservation.Command != "release:prepare" || record.PreparePRIntent == nil || record.Commit == nil {
			return StateV3MutationArm{}, false
		}
		return stateV3MutationArm(operation, record.Commit.Ref, "", record.Commit.OID, true, *record.PreparePRIntent)
	default:
		return StateV3MutationArm{}, false
	}
}

func stateV3ExpectedEventEvidenceDigest(record StateV3Record, event StateV3Event, index int) (string, bool) {
	var evidence any
	switch event.Kind {
	case StateV3EventReserved:
		evidence = record.Reservation
	case StateV3EventMutationArmed:
		armIndex := 0
		for prior := 0; prior < index; prior++ {
			if record.Events[prior].Kind == StateV3EventMutationArmed {
				armIndex++
			}
		}
		if armIndex >= len(record.MutationArms) {
			return "", false
		}
		evidence = record.MutationArms[armIndex]
	case StateV3EventPhaseAdvanced:
		switch event.Phase {
		case StateV3PhasePrepared:
			evidence = struct {
				Prepared *StateV3PreparedState `json:"prepared"`
				TagPlans []StateV3TagPlan      `json:"tag_plans"`
			}{record.Prepared, record.TagPlans}
		case StateV3PhaseBranchesPublished:
			if record.Prepared == nil {
				return "", false
			}
			evidence = struct {
				Commit   *StateV3AdoptedCommit   `json:"commit"`
				Branches []StateV3BranchEvidence `json:"branches"`
				Tags     []StateV3TagIntent      `json:"tags"`
			}{record.Commit, record.BranchEvidence, record.TagIntents}
		case StateV3PhaseTestsPassed:
			evidence = record.TestEvidence
		case StateV3PhaseTagsPublished:
			evidence = struct {
				Objects []StateV3TagObjectEvidence `json:"objects"`
				Refs    []StateV3TagEvidence       `json:"refs"`
			}{record.TagObjectEvidence, record.TagEvidence}
		case StateV3PhaseComplete:
			evidence = record.OutcomeEvidence
		default:
			return "", false
		}
	case StateV3EventPlatformCommitAdopted:
		evidence = struct {
			Commit *StateV3AdoptedCommit `json:"commit"`
			Tags   []StateV3TagIntent    `json:"tags"`
		}{record.Commit, record.TagIntents}
	case StateV3EventBranchPublished:
		branchIndex := 0
		for prior := 0; prior < index; prior++ {
			if record.Events[prior].Kind == StateV3EventBranchPublished {
				branchIndex++
			}
		}
		if branchIndex >= len(record.BranchEvidence) {
			return "", false
		}
		evidence = record.BranchEvidence[branchIndex]
	case StateV3EventTestsPassed:
		evidence = record.TestEvidence
	case StateV3EventTagObjectCreated:
		objectIndex := 0
		for prior := 0; prior < index; prior++ {
			if record.Events[prior].Kind == StateV3EventTagObjectCreated {
				objectIndex++
			}
		}
		if objectIndex >= len(record.TagObjectEvidence) {
			return "", false
		}
		evidence = record.TagObjectEvidence[objectIndex]
	case StateV3EventTagPublished:
		tagIndex := 0
		for prior := 0; prior < index; prior++ {
			if record.Events[prior].Kind == StateV3EventTagPublished {
				tagIndex++
			}
		}
		if tagIndex >= len(record.TagEvidence) {
			return "", false
		}
		evidence = record.TagEvidence[tagIndex]
	case StateV3EventPreparePRRecorded:
		evidence = record.PreparePREvidence
	case StateV3EventOutcomeRecorded:
		evidence = record.OutcomeEvidence
	default:
		return "", false
	}
	if evidence == nil || reflect.ValueOf(evidence).Kind() == reflect.Ptr && reflect.ValueOf(evidence).IsNil() {
		return "", false
	}
	raw, err := canonicalJSON(evidence)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), true
}

func emptyStateV3RefEvent(event StateV3Event) bool {
	return event.Ref == "" && event.ExpectedOldOID == "" && event.ObjectOID == "" && event.Disposition == ""
}

func matchesStateV3TagEvidence(evidence StateV3TagEvidence, event StateV3Event) bool {
	return event.Ref == evidence.Ref && event.ObjectOID == evidence.TagObjectOID && event.ExpectedOldOID == ""
}

func stateV3TargetRef(command, releaseLine string) (string, bool) {
	line, err := parseReleaseLine(releaseLine)
	if err != nil {
		return "", false
	}
	switch command {
	case "release:prepare":
		return "refs/heads/" + devBranchName(line.Major, line.Minor+1), true
	case "release:promote", "release:release":
		return "refs/heads/" + releaseBranchName(line.Major, line.Minor), true
	default:
		return "", false
	}
}

type stateV3Paths struct {
	Reservation string
	Prepared    string
	Bundle      string
}

func stateV3PreparedPaths(repositoryID, commentID string) (stateV3Paths, bool) {
	if !validID(repositoryID) || !validID(commentID) {
		return stateV3Paths{}, false
	}
	root := "requests/" + repositoryID + "/" + commentID + "/"
	return stateV3Paths{Reservation: root + stateV3ReservationFile, Prepared: root + stateV3PreparedFile, Bundle: root + stateV3GenerationBundleFile}, true
}

func stateV3CanonicalDigest(value any) (string, bool) {
	raw, err := canonicalJSON(value)
	if err != nil {
		return "", false
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), true
}

func stateV3FileChangesDigest(changes []StateV3FileChange) (string, error) {
	raw, err := canonicalJSON(changes)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func stateV3EventDigest(event StateV3Event) (string, error) {
	unsigned := event
	unsigned.Digest = ""
	raw, err := canonicalJSON(unsigned)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

// AppendStateV3RecordEvent binds an event to the canonical typed evidence in
// record. It does not persist the result or perform a GitHub mutation.
func AppendStateV3RecordEvent(record StateV3Record, event StateV3Event) (StateV3Record, error) {
	evidenceDigest, ok := stateV3ExpectedEventEvidenceDigest(record, event, len(record.Events))
	if !ok {
		return StateV3Record{}, newReleaseError(ErrorClassContractMismatch, "invalid_state_v3_event_evidence")
	}
	event.EvidenceSHA256 = evidenceDigest
	events, err := appendStateV3Event(record.Events, event)
	if err != nil {
		return StateV3Record{}, err
	}
	record.Events = events
	return record, nil
}

func appendStateV3Event(events []StateV3Event, event StateV3Event) ([]StateV3Event, error) {
	if len(events) >= 10_000 {
		return nil, newReleaseError(ErrorClassContractMismatch, "state_v3_event_limit")
	}
	event.Sequence = len(events) + 1
	event.PreviousDigest = ""
	if len(events) > 0 {
		event.PreviousDigest = events[len(events)-1].Digest
	}
	if !lowerHexDigest(event.EvidenceSHA256) {
		return nil, newReleaseError(ErrorClassContractMismatch, "invalid_state_v3_event")
	}
	digest, err := stateV3EventDigest(event)
	if err != nil {
		return nil, fmt.Errorf("digest state v3 event: %w", err)
	}
	event.Digest = digest
	return append(append([]StateV3Event(nil), events...), event), nil
}
