// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"fmt"
	"sort"
	"testing"
)

func TestStateV3RejectsReorderedTagsAndHistoryIncompatibleTagLimits(t *testing.T) {
	record := fixtureReleaseStateV3Record(t)
	if len(record.TagEvidence) < 2 {
		t.Fatal("release fixture needs multiple tags")
	}
	first := -1
	for index := range record.Events {
		if record.Events[index].Kind == StateV3EventTagPublished {
			if first < 0 {
				first = index
			} else {
				record.Events[first], record.Events[index] = record.Events[index], record.Events[first]
				break
			}
		}
	}
	rebindStateV3Events(t, &record)
	if err := validateV3Fixture(t, record); err == nil {
		t.Fatal("reordered tag publication events accepted")
	}

	batchedObjects := fixtureReleaseStateV3Record(t)
	firstObjectArm, firstObjectResult, firstRefArm, firstRefResult, secondObjectArm, secondObjectResult := -1, -1, -1, -1, -1, -1
	armIndex := 0
	for eventIndex, event := range batchedObjects.Events {
		if event.Kind == StateV3EventMutationArmed {
			kind := batchedObjects.MutationArms[armIndex].OperationKind
			if kind == "tag_object_create" && firstObjectArm < 0 {
				firstObjectArm = eventIndex
			} else if kind == "tag_ref_create" && firstRefArm < 0 {
				firstRefArm = eventIndex
			} else if kind == "tag_object_create" && secondObjectArm < 0 {
				secondObjectArm = eventIndex
			}
			armIndex++
		} else if event.Kind == StateV3EventTagObjectCreated {
			if firstObjectResult < 0 {
				firstObjectResult = eventIndex
			} else if secondObjectResult < 0 {
				secondObjectResult = eventIndex
			}
		} else if event.Kind == StateV3EventTagPublished && firstRefResult < 0 {
			firstRefResult = eventIndex
		}
	}
	if firstObjectArm < 0 || firstObjectResult < 0 || firstRefArm < 0 || firstRefResult < 0 || secondObjectArm < 0 || secondObjectResult < 0 {
		t.Fatal("release fixture lacks two tag groups")
	}
	// Move object 1's arm/result ahead of ref 0's arm/result, and mirror the arm array.
	events := append([]StateV3Event(nil), batchedObjects.Events...)
	block := append([]StateV3Event(nil), events[secondObjectArm:secondObjectResult+1]...)
	events = append(events[:secondObjectArm], events[secondObjectResult+1:]...)
	events = append(events[:firstRefArm], append(block, events[firstRefArm:]...)...)
	batchedObjects.Events = events
	batchedObjects.MutationArms[2], batchedObjects.MutationArms[3] = batchedObjects.MutationArms[3], batchedObjects.MutationArms[2]
	rebindStateV3Events(t, &batchedObjects)
	if err := validateV3Fixture(t, batchedObjects); err == nil {
		t.Fatal("next tag object accepted before prior tag ref result")
	}

	if 4*MaxStateV3Tags+StateV3PrepareHistoryOverhead > MaxStateV3HistoryCommits {
		t.Fatal("maximum tag count cannot fit the worst-case authenticated prepare history")
	}
	policy := fixtureStateV3Policy()
	base := fixtureStateV3Record(t)
	makePlans := func(total int) []StateV3TagPlan {
		plans := make([]StateV3TagPlan, 0, total)
		for index := 0; index < total-1; index++ {
			name := fmt.Sprintf("module-%04d/%s", index, base.Reservation.GenerationVersion)
			plans = append(plans, StateV3TagPlan{Name: name, Ref: "refs/tags/" + name, Message: name + "\n", TargetRef: base.Prepared.Mutation.TargetRef, Tagger: policy.Tagger, TaggerDate: "2026-09-12T06:30:00Z", Signature: "absent"})
		}
		plans = append(plans, StateV3TagPlan{Name: base.Reservation.GenerationVersion, Ref: "refs/tags/" + base.Reservation.GenerationVersion, Message: base.Reservation.GenerationVersion + "\n", TargetRef: base.Prepared.Mutation.TargetRef, Tagger: policy.Tagger, TaggerDate: "2026-09-12T06:30:00Z", Signature: "absent"})
		sort.Slice(plans, func(i, j int) bool { return plans[i].Ref < plans[j].Ref })
		return plans
	}
	otherBound := min(MaxStateV3Tags, (policy.StateLanes.Minor.MaxHistoryCommits-StateV3OtherHistoryOverhead)/4)
	if !validStateV3TagPlans(makePlans(otherBound), base.Prepared.Mutation, base.Reservation, policy) || validStateV3TagPlans(makePlans(otherBound+1), base.Prepared.Mutation, base.Reservation, policy) {
		t.Fatal("promote/release tag history bound is not exact")
	}
	prepareMutation := cloneStateV3(t, base.Prepared.Mutation)
	prepareMutation.Branches = append(prepareMutation.Branches, StateV3BranchMutationIntent{}, StateV3BranchMutationIntent{})
	policy.StateLanes.Minor.MaxHistoryCommits = 50
	prepareBound := min(MaxStateV3Tags, (policy.StateLanes.Minor.MaxHistoryCommits-StateV3PrepareHistoryOverhead)/4)
	if !validStateV3TagPlans(makePlans(prepareBound), prepareMutation, base.Reservation, policy) || validStateV3TagPlans(makePlans(prepareBound+1), prepareMutation, base.Reservation, policy) {
		t.Fatal("prepare tag history bound is not exact")
	}
}

func TestStateV3ZeroAttemptReconciliationAndDispositionConsistency(t *testing.T) {
	for name, mutate := range map[string]func(*StateV3Record){
		"commit": func(record *StateV3Record) {
			record.Commit.MutationResponse = StateV3MutationResponse{Observation: "not_attempted"}
			record.BranchEvidence[len(record.BranchEvidence)-1].Response = record.Commit.MutationResponse
			record.BranchEvidence[len(record.BranchEvidence)-1].Disposition = "reconciled"
			removeStateV3MutationArm(t, record, "platform_commit", 0)
			for index := range record.Events {
				if record.Events[index].Kind == StateV3EventPlatformCommitAdopted || record.Events[index].Kind == StateV3EventBranchPublished {
					record.Events[index].Disposition = "reconciled"
				}
			}
		},
		"tag object": func(record *StateV3Record) {
			record.TagObjectEvidence[0].Response = StateV3MutationResponse{Observation: "not_attempted"}
			removeStateV3MutationArm(t, record, "tag_object_create", 0)
			for index := range record.Events {
				if record.Events[index].Kind == StateV3EventTagObjectCreated {
					record.Events[index].Disposition = "reconciled"
				}
			}
		},
		"tag object and ref": func(record *StateV3Record) {
			record.TagObjectEvidence[0].Response = StateV3MutationResponse{Observation: "not_attempted"}
			record.TagEvidence[0].RefResponse = StateV3MutationResponse{Observation: "not_attempted"}
			removeStateV3MutationArm(t, record, "tag_object_create", 0)
			removeStateV3MutationArm(t, record, "tag_ref_create", 0)
			for index := range record.Events {
				if record.Events[index].Kind == StateV3EventTagObjectCreated || record.Events[index].Kind == StateV3EventTagPublished {
					record.Events[index].Disposition = "reconciled"
				}
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			record := fixtureStateV3Record(t)
			mutate(&record)
			rebindStateV3Events(t, &record)
			if err := validateV3Fixture(t, record); err != nil {
				t.Fatalf("exact zero-attempt reconciliation rejected: %v", err)
			}
		})
	}
	for name, mutate := range map[string]func(*StateV3Record){
		"zero attempt observed": func(record *StateV3Record) { record.Commit.MutationResponse.Attempts = 0 },
		"not attempted with oid": func(record *StateV3Record) {
			record.Commit.MutationResponse = StateV3MutationResponse{Observation: "not_attempted", OID: record.Commit.OID}
		},
		"not attempted with attempt": func(record *StateV3Record) {
			record.Commit.MutationResponse = StateV3MutationResponse{Observation: "not_attempted", Attempts: 1}
		},
		"commit disposition": func(record *StateV3Record) {
			record.Commit.MutationResponse = StateV3MutationResponse{Observation: "not_attempted"}
		},
		"tag disposition": func(record *StateV3Record) {
			record.TagObjectEvidence[0].Response = StateV3MutationResponse{Observation: "not_attempted"}
			record.TagEvidence[0].RefResponse = StateV3MutationResponse{Observation: "not_attempted"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			record := fixtureStateV3Record(t)
			mutate(&record)
			rebindStateV3Events(t, &record)
			if err := validateV3Fixture(t, record); err == nil {
				t.Fatal("inconsistent zero-attempt evidence accepted")
			}
		})
	}
}

func TestStateV3GitHubBotIdentityAndIndependentAPIObservations(t *testing.T) {
	if !validStateV3Email("41898282+github-actions[bot]@users.noreply.github.com") {
		t.Fatal("observed GitHub bot noreply email rejected")
	}
	for _, email := range []string{"a@@users.noreply.github.com", "a <b>@users.noreply.github.com", "a>b@users.noreply.github.com", "a\tb@users.noreply.github.com", "a\nb@users.noreply.github.com"} {
		if validStateV3Email(email) {
			t.Fatalf("ambiguous Git email accepted: %q", email)
		}
	}
	policy := fixtureStateV3Policy()
	if policy.CommitRoles.AuthorREST.Type == policy.CommitRoles.AuthorGraphQL.Type {
		t.Fatal("fixture normalized distinct REST Bot and GraphQL User observations")
	}
	policy.CommitRoles.CommitterREST = StateV3OptionalAssociatedIdentity{}
	policy.CommitRoles.CommitterGraphQL = StateV3OptionalAssociatedIdentity{Present: true, Identity: StateV3AssociatedIdentity{Login: "synthetic-platform-committer", DatabaseID: "40004", Type: "User"}}
	if _, err := DecodeStateV3Policy(mustJSON(t, policy)); err != nil {
		t.Fatalf("independent nullable committer observations rejected: %v", err)
	}
	policy = fixtureStateV3Policy()
	policy.CommitRoles.AuthorGraphQL.DatabaseID = "99999"
	if _, err := DecodeStateV3Policy(mustJSON(t, policy)); err == nil {
		t.Fatal("disagreeing REST and GraphQL author principals accepted")
	}
}
