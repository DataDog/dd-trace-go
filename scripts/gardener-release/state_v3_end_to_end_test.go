// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"testing"
)

func TestStateV3PrepareDerivesMainSourceAndDevelopmentTarget(t *testing.T) {
	record := fixtureStateV3Record(t)
	record.Reservation.Command = "release:prepare"
	record.Reservation.RequestedVersion = "v2.11.0"
	record.Reservation.ResolvedVersion = "v2.11.0"
	record.Reservation.DevelopmentVersion = "v2.12.0-dev"
	record.Reservation.GenerationVersion = "v2.12.0-dev"
	record.Reservation.BodySnapshot = "/gardener release:prepare v2.11.0"
	record.Reservation.Marker = Marker("123", "789", "release:prepare", "v2.11.0")
	requestContext := Context{
		RepositoryID: "123", RepositoryFullName: RepositoryFullName, IssueNumber: "456",
		OriginalCommentID: "789", AcknowledgementCommentID: "790",
		BodySnapshot: record.Reservation.BodySnapshot, PolicyRevision: record.Reservation.PolicyRevision,
	}
	record.Reservation.RequestSHA256 = RequestSHA256(requestContext, "release:prepare", "v2.11.0")
	record.Binding.RequestSHA256 = record.Reservation.RequestSHA256
	record.Reservation.SourceRef = "refs/heads/main"
	fixtureStateV3VersionResolution(t, &record.Reservation, "v2.11.0-dev")
	record.Prepared.Mutation.TargetRef = "refs/heads/dev-v2.12.x"
	record.Prepared.Mutation.Message = "release: v2.12.0-dev"
	record.Prepared.Mutation.Branches = []StateV3BranchMutationIntent{
		{Ref: "refs/heads/release-v2.11.x", Target: "source"},
		{Ref: "refs/heads/dev-v2.12.x", Target: "source"},
		{Ref: "refs/heads/dev-v2.12.x", ExpectedOldOID: record.Reservation.SourceOID, Target: "platform_commit"},
	}
	record.TagPlans[0].Name = "v2.12.0-dev"
	record.TagPlans[0].Ref = "refs/tags/v2.12.0-dev"
	record.TagPlans[0].Message = "v2.12.0-dev\n"
	record.TagPlans[0].TargetRef = record.Prepared.Mutation.TargetRef
	record.TagIntents[0].Name = record.TagPlans[0].Name
	record.TagIntents[0].Ref = record.TagPlans[0].Ref
	record.TagIntents[0].Message = record.TagPlans[0].Message
	tagObjectOID, ok := stateV3TagObjectOID(record.TagPlans[0], record.Commit.OID)
	if !ok {
		t.Fatal("could not calculate prepare tag object OID")
	}
	record.TagIntents[0].ExpectedTagObjectOID = tagObjectOID
	record.TagObjectEvidence[0].Intent = record.TagIntents[0]
	record.TagObjectEvidence[0].RESTTagObjectOID = tagObjectOID
	record.TagObjectEvidence[0].PeeledCommitOID = record.Commit.OID
	record.TagEvidence[0].Name = record.TagIntents[0].Name
	record.TagEvidence[0].Ref = record.TagIntents[0].Ref
	record.TagEvidence[0].Message = record.TagIntents[0].Message
	record.TagEvidence[0].TagObjectOID = tagObjectOID
	record.TagObjectEvidence[0].Response.OID = tagObjectOID
	record.TagEvidence[0].RefResponse.OID = tagObjectOID
	record.TagEvidence[0].ObservedRefOID = tagObjectOID
	record.TagEvidence[0].RESTTagObjectOID = tagObjectOID
	record.Commit.Ref = record.Prepared.Mutation.TargetRef
	record.Commit.Message = record.Prepared.Mutation.Message
	record.BranchEvidence = []StateV3BranchEvidence{
		{Intent: record.Prepared.Mutation.Branches[0], Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: record.Reservation.SourceOID}, ObjectOID: record.Reservation.SourceOID, ObservedRefOID: record.Reservation.SourceOID, Status: "present", Disposition: "published"},
		{Intent: record.Prepared.Mutation.Branches[1], Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: record.Reservation.SourceOID}, ObjectOID: record.Reservation.SourceOID, ObservedRefOID: record.Reservation.SourceOID, Status: "present", Disposition: "published"},
		{Intent: record.Prepared.Mutation.Branches[2], Response: record.Commit.MutationResponse, ObjectOID: record.Commit.OID, ObservedRefOID: record.Commit.OID, Status: "present", Disposition: "published"},
	}
	record.TestEvidence.Runs = []StateV3TestRunEvidence{
		{TargetBranch: "release-v2.11.x", TargetOID: record.Reservation.SourceOID, RunID: "80001", Attempt: 1, Status: "completed", Conclusion: "success", Jobs: []StateV3TestJobEvidence{{ID: "81001", Name: "required-a", Attempt: 1, Status: "completed", Conclusion: "success"}, {ID: "81002", Name: "required-b", Attempt: 1, Status: "completed", Conclusion: "success"}}},
		{TargetBranch: "dev-v2.12.x", TargetOID: record.Commit.OID, RunID: "80002", Attempt: 1, Status: "completed", Conclusion: "success", Jobs: []StateV3TestJobEvidence{{ID: "82001", Name: "required-a", Attempt: 1, Status: "completed", Conclusion: "success"}, {ID: "82002", Name: "required-b", Attempt: 1, Status: "completed", Conclusion: "success"}}},
	}
	record.PreparePRIntent = &StateV3PreparePRIntent{Repository: RepositoryFullName, Base: "main", Head: "dev-v2.12.x", HeadOID: record.Commit.OID, Title: "Prepare v2.12.0-dev", BodyMarker: preparePRMarker(record.Reservation.RequestKey), ExpectedAbsent: true}
	record.PreparePREvidence = &StateV3PreparePREvidence{SchemaVersion: "1", Intent: *record.PreparePRIntent, Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: record.Commit.OID}, Number: "42", URL: "https://github.com/DataDog/dd-trace-go/pull/42", ObservedHead: "dev-v2.12.x", ObservedOID: record.Commit.OID, ObservedBase: "main", ObservedMarker: preparePRMarker(record.Reservation.RequestKey), Status: "open", Disposition: "created"}
	record.OutcomeEvidence.Command = "release:prepare"
	record.OutcomeEvidence.PreparePR = WorkSucceeded
	finalizeStateV3PreparedFixture(t, record.Prepared, record.Reservation, record.TagPlans)
	record.OutcomeEvidence.PriorStateSHA256, _ = stateV3OutcomePriorStateDigest(record)
	record.Events = nil
	record.MutationArms = nil
	appendEvent := func(event StateV3Event) {
		event.EvidenceSHA256, _ = stateV3ExpectedEventEvidenceDigest(record, event, len(record.Events))
		var err error
		record.Events, err = appendStateV3Event(record.Events, event)
		if err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(StateV3Event{Kind: StateV3EventReserved, Phase: StateV3PhaseReserved})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhasePrepared})
	for index := 0; index < 2; index++ {
		intent := record.Prepared.Mutation.Branches[index]
		arm, _ := stateV3MutationArm("branch_ref_create", intent.Ref, intent.ExpectedOldOID, record.Reservation.SourceOID, true, intent)
		record.MutationArms = append(record.MutationArms, arm)
		appendEvent(StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhasePrepared, Ref: arm.Ref, ObjectOID: arm.IntendedObjectOID, Disposition: "armed"})
		appendEvent(StateV3Event{Kind: StateV3EventBranchPublished, Phase: StateV3PhasePrepared, Ref: intent.Ref, ObjectOID: record.Reservation.SourceOID, Disposition: "published"})
	}
	commitArm, _ := stateV3MutationArm("platform_commit", record.Prepared.Mutation.TargetRef, record.Prepared.Mutation.ExpectedHeadOID, "", false, record.Prepared.Mutation)
	record.MutationArms = append(record.MutationArms, commitArm)
	appendEvent(StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhasePrepared, Ref: commitArm.Ref, ExpectedOldOID: commitArm.ExpectedOldOID, Disposition: "armed"})
	appendEvent(StateV3Event{Kind: StateV3EventPlatformCommitAdopted, Phase: StateV3PhasePrepared, Ref: record.Commit.Ref, ExpectedOldOID: record.Commit.ParentOID, ObjectOID: record.Commit.OID, Disposition: "adopted"})
	appendEvent(StateV3Event{Kind: StateV3EventBranchPublished, Phase: StateV3PhasePrepared, Ref: record.Commit.Ref, ExpectedOldOID: record.Commit.ParentOID, ObjectOID: record.Commit.OID, Disposition: "published"})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhaseBranchesPublished})
	appendEvent(StateV3Event{Kind: StateV3EventTestsPassed, Phase: StateV3PhaseBranchesPublished})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhaseTestsPassed})
	objectArm, _ := stateV3MutationArm("tag_object_create", record.TagIntents[0].Ref, "", tagObjectOID, true, record.TagIntents[0])
	record.MutationArms = append(record.MutationArms, objectArm)
	appendEvent(StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhaseTestsPassed, Ref: objectArm.Ref, ObjectOID: objectArm.IntendedObjectOID, Disposition: "armed"})
	appendEvent(StateV3Event{Kind: StateV3EventTagObjectCreated, Phase: StateV3PhaseTestsPassed, Ref: objectArm.Ref, ObjectOID: tagObjectOID, Disposition: "created"})
	refIntent := struct {
		Ref       string `json:"ref"`
		ObjectOID string `json:"object_oid"`
	}{record.TagIntents[0].Ref, tagObjectOID}
	refArm, _ := stateV3MutationArm("tag_ref_create", record.TagIntents[0].Ref, "", tagObjectOID, true, refIntent)
	record.MutationArms = append(record.MutationArms, refArm)
	appendEvent(StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhaseTestsPassed, Ref: refArm.Ref, ObjectOID: refArm.IntendedObjectOID, Disposition: "armed"})
	appendEvent(StateV3Event{Kind: StateV3EventTagPublished, Phase: StateV3PhaseTestsPassed, Ref: record.TagEvidence[0].Ref, ObjectOID: tagObjectOID, Disposition: "published"})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhaseTagsPublished})
	prArm, _ := stateV3MutationArm("prepare_pr_create", record.Commit.Ref, "", record.Commit.OID, true, *record.PreparePRIntent)
	record.MutationArms = append(record.MutationArms, prArm)
	appendEvent(StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhaseTagsPublished, Ref: prArm.Ref, ObjectOID: prArm.IntendedObjectOID, Disposition: "armed"})
	appendEvent(StateV3Event{Kind: StateV3EventPreparePRRecorded, Phase: StateV3PhaseTagsPublished, Ref: record.Commit.Ref, ObjectOID: record.Commit.OID, Disposition: "created"})
	record.OutcomeEvidence.PriorStateSHA256, _ = stateV3OutcomePriorStateDigest(record)
	appendEvent(StateV3Event{Kind: StateV3EventOutcomeRecorded, Phase: StateV3PhaseTagsPublished})
	appendEvent(StateV3Event{Kind: StateV3EventPhaseAdvanced, Phase: StateV3PhaseComplete})
	if err := validateV3Fixture(t, record); err != nil {
		t.Fatalf("derived prepare target rejected: %v", err)
	}
	prematurePlatform := fixtureStateV3RecordAtEventCount(t, record, 2)
	prematureArm, _ := stateV3MutationArm("platform_commit", prematurePlatform.Prepared.Mutation.TargetRef, prematurePlatform.Prepared.Mutation.ExpectedHeadOID, "", false, prematurePlatform.Prepared.Mutation)
	prematurePlatform.MutationArms = append(prematurePlatform.MutationArms, prematureArm)
	prematurePlatform, _ = AppendStateV3RecordEvent(prematurePlatform, StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhasePrepared, Ref: prematureArm.Ref, ExpectedOldOID: prematureArm.ExpectedOldOID, Disposition: "armed"})
	if err := validateV3Fixture(t, prematurePlatform); err == nil {
		t.Fatal("prepare platform commit armed before both required source branch results")
	}
	prArmEvent := -1
	for index, event := range record.Events {
		if event.Kind == StateV3EventMutationArmed {
			armCount := 0
			for prior := 0; prior <= index; prior++ {
				if record.Events[prior].Kind == StateV3EventMutationArmed {
					armCount++
				}
			}
			if record.MutationArms[armCount-1].OperationKind == "prepare_pr_create" {
				prArmEvent = index
				break
			}
		}
	}
	if prArmEvent < 0 {
		t.Fatal("prepare PR arm missing")
	}
	prCrash := fixtureStateV3RecordAtEventCount(t, record, prArmEvent+1)
	if err := validateV3Fixture(t, prCrash); err != nil {
		t.Fatalf("terminal ambiguous prepare PR arm rejected: %v", err)
	}
	prCrashAuth := fixtureStateV3Authentication(t, fixtureStateV3Policy(), prCrash)
	remainingPR, knownPR := stateV3RemainingOperationCommits(prCrash, prCrashAuth.Current)
	depthPR := len(prCrashAuth.Predecessors) + 1
	if !knownPR || !stateV3CapacityFits(depthPR, remainingPR, depthPR+remainingPR) || stateV3CapacityFits(depthPR, remainingPR, depthPR+remainingPR-1) {
		t.Fatal("pending prepare PR exact/one-short capacity boundary is not closed")
	}
	for name, response := range map[string]StateV3MutationResponse{
		"observed": {Observation: "observed", Attempts: 1, OID: record.Commit.OID},
		"lost":     {Observation: "lost", Attempts: 1},
	} {
		t.Run("prepare PR "+name, func(t *testing.T) {
			candidate := cloneStateV3(t, record)
			candidate.PreparePREvidence.Response = response
			rebindStateV3Events(t, &candidate)
			if err := validateV3Fixture(t, candidate); err != nil {
				t.Fatalf("typed prepare PR response rejected: %v", err)
			}
		})
	}
	t.Run("prepare PR zero attempt", func(t *testing.T) {
		candidate := cloneStateV3(t, record)
		candidate.PreparePREvidence.Response = StateV3MutationResponse{Observation: "not_attempted"}
		candidate.PreparePREvidence.Disposition = "reconciled"
		removeStateV3MutationArm(t, &candidate, "prepare_pr_create", 0)
		for index := range candidate.Events {
			if candidate.Events[index].Kind == StateV3EventPreparePRRecorded {
				candidate.Events[index].Disposition = "reconciled"
			}
		}
		rebindStateV3Events(t, &candidate)
		if err := validateV3Fixture(t, candidate); err != nil {
			t.Fatalf("exact zero-attempt prepare PR reconciliation rejected: %v", err)
		}
	})
	for name, mutate := range map[string]func(*StateV3Record){
		"prepare PR absent reread":  func(candidate *StateV3Record) { candidate.PreparePREvidence.ObservedOID = "" },
		"prepare PR wrong status":   func(candidate *StateV3Record) { candidate.PreparePREvidence.Status = "closed" },
		"prepare PR wrong marker":   func(candidate *StateV3Record) { candidate.PreparePREvidence.ObservedMarker = "wrong" },
		"prepare PR second attempt": func(candidate *StateV3Record) { candidate.PreparePREvidence.Response.Attempts = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneStateV3(t, record)
			mutate(&candidate)
			rebindStateV3Events(t, &candidate)
			if err := validateV3Fixture(t, candidate); err == nil {
				t.Fatal("invalid prepare PR mutation evidence accepted")
			}
		})
	}
	for name, reorder := range map[string]func([]StateV3Event) []StateV3Event{
		"adoption before source branches": func(events []StateV3Event) []StateV3Event {
			return append(append(append([]StateV3Event{}, events[:2]...), events[4]), append(events[2:4], events[5:]...)...)
		},
		"platform branch before adoption": func(events []StateV3Event) []StateV3Event {
			result := append([]StateV3Event(nil), events...)
			result[4], result[5] = result[5], result[4]
			return result
		},
		"development source after adoption": func(events []StateV3Event) []StateV3Event {
			result := append([]StateV3Event(nil), events...)
			result[3], result[4] = result[4], result[3]
			return result
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneStateV3(t, record)
			candidate.Events = reorder(candidate.Events)
			rebindStateV3Events(t, &candidate)
			if err := validateV3Fixture(t, candidate); err == nil {
				t.Fatal("hostile prepare event ordering accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*StateV3Record){
		"PR release oid":     func(candidate *StateV3Record) { candidate.PreparePREvidence.ObservedOID = v3OIDd },
		"PR head":            func(candidate *StateV3Record) { candidate.PreparePREvidence.ObservedHead = "release-v2.11.x" },
		"PR outcome missing": func(candidate *StateV3Record) { candidate.OutcomeEvidence.PreparePR = WorkNotApplicable },
		"substituted dev number": func(candidate *StateV3Record) {
			candidate.Reservation.DevelopmentVersion = "v2.12.0-dev.99"
			candidate.Reservation.GenerationVersion = candidate.Reservation.DevelopmentVersion
			candidate.Reservation.VersionResolution.Result.DevelopmentVersion = candidate.Reservation.DevelopmentVersion
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneStateV3(t, record)
			mutate(&candidate)
			rebindStateV3Events(t, &candidate)
			if err := validateV3Fixture(t, candidate); err == nil {
				t.Fatal("invalid prepare PR or outcome evidence accepted")
			}
		})
	}
	record.Reservation.SourceRef = "refs/heads/caller"
	if err := validateV3Fixture(t, record); err == nil {
		t.Fatal("caller-controlled prepare source accepted")
	}
}
