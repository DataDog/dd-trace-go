// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestStateV3RejectsUnreconciledPlatformCommitAndCallerIdentity(t *testing.T) {
	base := fixtureStateV3Record(t)
	for name, mutate := range map[string]func(*StateV3Record){
		"response oid":              func(r *StateV3Record) { r.Commit.MutationResponse.OID = v3OIDd },
		"ref oid":                   func(r *StateV3Record) { r.Commit.ObservedRefOID = v3OIDd },
		"rest oid":                  func(r *StateV3Record) { r.Commit.RESTOID = v3OIDd },
		"graphql oid":               func(r *StateV3Record) { r.Commit.GraphQLOID = v3OIDd },
		"parent":                    func(r *StateV3Record) { r.Commit.ParentOID = v3OIDb },
		"tree":                      func(r *StateV3Record) { r.Commit.TreeOID = v3OIDd },
		"ref":                       func(r *StateV3Record) { r.Commit.Ref = "refs/heads/caller" },
		"not verified":              func(r *StateV3Record) { r.Commit.RESTVerified = false },
		"wrong verification reason": func(r *StateV3Record) { r.Commit.RESTReason = "unsigned" },
		"invalid graphql signature": func(r *StateV3Record) { r.Commit.GraphQLSignatureValid = false },
		"not github signed":         func(r *StateV3Record) { r.Commit.WasSignedByGitHub = false },
		"wrong signature state":     func(r *StateV3Record) { r.Commit.SignatureState = "UNKNOWN" },
		"changed file identity":     func(r *StateV3Record) { r.Commit.FileChanges[0].BlobOID = v3OIDd },
		"platform branch reread":    func(r *StateV3Record) { r.BranchEvidence[len(r.BranchEvidence)-1].ObservedRefOID = v3OIDd },
		"caller author":             func(r *StateV3Record) { r.Commit.Roles.AuthorREST.Login = "caller" },
	} {
		t.Run(name, func(t *testing.T) {
			record := cloneStateV3(t, base)
			mutate(&record)
			if err := validateV3Fixture(t, record); err == nil {
				t.Fatal("unreconciled platform commit accepted")
			}
		})
	}
}

func TestStateV3AdoptsExactIndependentEvidenceWhenMutationResponsesAreLost(t *testing.T) {
	for name, mutate := range map[string]func(*StateV3Record){
		"commit response lost": func(record *StateV3Record) {
			record.Commit.MutationResponse = StateV3MutationResponse{Observation: "lost", Attempts: 1}
			record.BranchEvidence[len(record.BranchEvidence)-1].Response = record.Commit.MutationResponse
		},
		"tag object response lost": func(record *StateV3Record) {
			record.TagObjectEvidence[0].Response = StateV3MutationResponse{Observation: "lost", Attempts: 1}
		},
		"tag ref response lost": func(record *StateV3Record) {
			record.TagEvidence[0].RefResponse = StateV3MutationResponse{Observation: "lost", Attempts: 1}
		},
		"all mutation responses lost": func(record *StateV3Record) {
			record.Commit.MutationResponse = StateV3MutationResponse{Observation: "lost", Attempts: 1}
			record.BranchEvidence[len(record.BranchEvidence)-1].Response = record.Commit.MutationResponse
			record.TagObjectEvidence[0].Response = StateV3MutationResponse{Observation: "lost", Attempts: 1}
			record.TagEvidence[0].RefResponse = StateV3MutationResponse{Observation: "lost", Attempts: 1}
		},
	} {
		t.Run(name, func(t *testing.T) {
			record := fixtureStateV3Record(t)
			mutate(&record)
			rebindStateV3Events(t, &record)
			if err := validateV3Fixture(t, record); err != nil {
				t.Fatalf("exact read-only reconciliation rejected: %v", err)
			}
		})
	}
	for name, mutate := range map[string]func(*StateV3Record){
		"commit retried":     func(record *StateV3Record) { record.Commit.MutationResponse.Attempts = 2 },
		"tag object retried": func(record *StateV3Record) { record.TagObjectEvidence[0].Response.Attempts = 2 },
		"tag ref retried":    func(record *StateV3Record) { record.TagEvidence[0].RefResponse.Attempts = 2 },
		"lost response with oid": func(record *StateV3Record) {
			record.Commit.MutationResponse = StateV3MutationResponse{Observation: "lost", Attempts: 1, OID: record.Commit.OID}
		},
	} {
		t.Run(name, func(t *testing.T) {
			record := fixtureStateV3Record(t)
			mutate(&record)
			rebindStateV3Events(t, &record)
			if err := validateV3Fixture(t, record); err == nil {
				t.Fatal("ambiguous or repeated mutation accepted")
			}
		})
	}
}

func TestStateV3RejectsSignedLightweightChainedOrConflictingTags(t *testing.T) {
	base := fixtureStateV3Record(t)
	for name, mutate := range map[string]func(*StateV3TagPlan){
		"signed plan":      func(tag *StateV3TagPlan) { tag.Signature = "present" },
		"signature block":  func(tag *StateV3TagPlan) { tag.Message += "-----BEGIN PGP SIGNATURE-----\n" },
		"wrong target ref": func(tag *StateV3TagPlan) { tag.TargetRef = "refs/heads/caller" },
		"wrong message":    func(tag *StateV3TagPlan) { tag.Message = "alternate\n" },
		"caller tagger":    func(tag *StateV3TagPlan) { tag.Tagger.Email = "caller@example.invalid" },
		"ambiguous date":   func(tag *StateV3TagPlan) { tag.TaggerDate = "2026-09-12T08:30:00+02:00" },
	} {
		t.Run(name, func(t *testing.T) {
			record := cloneStateV3(t, base)
			mutate(&record.TagPlans[0])
			if err := validateV3Fixture(t, record); err == nil {
				t.Fatal("invalid tag plan accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*StateV3TagIntent){
		"signed intent":           func(tag *StateV3TagIntent) { tag.Signature = "present" },
		"lightweight intent":      func(tag *StateV3TagIntent) { tag.ObjectType = "commit" },
		"tag chain intent":        func(tag *StateV3TagIntent) { tag.TargetType = "tag" },
		"wrong target":            func(tag *StateV3TagIntent) { tag.TargetOID = v3OIDa },
		"wrong deterministic oid": func(tag *StateV3TagIntent) { tag.ExpectedTagObjectOID = v3OIDa },
		"wrong tagger date":       func(tag *StateV3TagIntent) { tag.TaggerDate = "2026-09-12T06:30:01Z" },
	} {
		t.Run(name, func(t *testing.T) {
			record := cloneStateV3(t, base)
			mutate(&record.TagIntents[0])
			if err := validateV3Fixture(t, record); err == nil {
				t.Fatal("invalid finalized tag intent accepted")
			}
		})
	}
	for name, mutate := range map[string]func(*StateV3TagEvidence){
		"signed evidence":      func(tag *StateV3TagEvidence) { tag.Signature = "present" },
		"lightweight evidence": func(tag *StateV3TagEvidence) { tag.ObjectType = "commit" },
		"tag chain evidence":   func(tag *StateV3TagEvidence) { tag.TargetType = "tag" },
		"wrong ref oid":        func(tag *StateV3TagEvidence) { tag.ObservedRefOID = v3OIDa },
		"wrong reread oid":     func(tag *StateV3TagEvidence) { tag.RESTTagObjectOID = v3OIDa },
		"wrong target":         func(tag *StateV3TagEvidence) { tag.TargetOID = v3OIDa },
		"wrong peel":           func(tag *StateV3TagEvidence) { tag.PeeledCommitOID = v3OIDa },
		"wrong reread message": func(tag *StateV3TagEvidence) { tag.Message = "alternate\n" },
		"wrong reread tagger":  func(tag *StateV3TagEvidence) { tag.Tagger.Email = "caller@example.invalid" },
		"wrong reread date":    func(tag *StateV3TagEvidence) { tag.TaggerDate = "2026-09-12T06:30:01Z" },
	} {
		t.Run(name, func(t *testing.T) {
			record := cloneStateV3(t, base)
			mutate(&record.TagEvidence[0])
			if err := validateV3Fixture(t, record); err == nil {
				t.Fatal("invalid tag evidence accepted")
			}
		})
	}
}

func TestStateV3TagOIDRejectsGitIdentityDelimiterInjection(t *testing.T) {
	record := fixtureStateV3Record(t)
	for name, mutate := range map[string]func(*StateV3TagPlan){
		"name less-than":    func(plan *StateV3TagPlan) { plan.Tagger.Name = "Release <spoof" },
		"name greater-than": func(plan *StateV3TagPlan) { plan.Tagger.Name = "Release >spoof" },
		"email delimiter":   func(plan *StateV3TagPlan) { plan.Tagger.Email = "release>spoof@example.invalid" },
		"unicode control":   func(plan *StateV3TagPlan) { plan.Tagger.Name = "Release\u0085App" },
		"ambiguous email":   func(plan *StateV3TagPlan) { plan.Tagger.Email = "a@@example.invalid" },
	} {
		t.Run(name, func(t *testing.T) {
			plan := cloneStateV3(t, record.TagPlans[0])
			mutate(&plan)
			if _, ok := stateV3TagObjectOID(plan, record.Commit.OID); ok {
				t.Fatal("ambiguous Git tagger identity accepted")
			}
		})
	}
}

func TestStateV3RejectsPhaseSkipsRegressionsAndPerRefEventAmbiguity(t *testing.T) {
	base := fixtureStateV3Record(t)
	for name, mutate := range map[string]func(*StateV3Record){
		"phase skip": func(r *StateV3Record) {
			r.Events[1].Phase = StateV3PhaseBranchesPublished
			r.Events = rechainStateV3Events(t, r.Events)
		},
		"phase regression": func(r *StateV3Record) {
			r.Events[4].Phase = StateV3PhaseReserved
			r.Events = rechainStateV3Events(t, r.Events)
		},
		"branch wrong object": func(r *StateV3Record) { r.Events[3].ObjectOID = v3OIDd; r.Events = rechainStateV3Events(t, r.Events) },
		"platform branch before adoption": func(r *StateV3Record) {
			r.Events[2], r.Events[3] = r.Events[3], r.Events[2]
			rebindStateV3Events(t, r)
		},
		"tag wrong ref": func(r *StateV3Record) {
			r.Events[7].Ref = "refs/tags/caller"
			r.Events = rechainStateV3Events(t, r.Events)
		},
		"missing tag event": func(r *StateV3Record) {
			r.Events = append(r.Events[:7], r.Events[8:]...)
			r.Events = rechainStateV3Events(t, r.Events)
		},
		"event chain tamper": func(r *StateV3Record) { r.Events[3].ObjectOID = v3OIDd },
	} {
		t.Run(name, func(t *testing.T) {
			record := cloneStateV3(t, base)
			mutate(&record)
			if err := validateV3Fixture(t, record); err == nil {
				t.Fatal("invalid event progression accepted")
			}
		})
	}
}

func rewriteStateV3SnapshotRecord(t *testing.T, snapshot *StateV3StateSnapshot, record StateV3Record) {
	t.Helper()
	raw, err := canonicalJSON(record)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	snapshot.RawRecord = raw
	snapshot.RecordSHA256 = hex.EncodeToString(digest[:])
	snapshot.RecordBlobOID = stateV3GitBlobOID(raw)
	for index := range snapshot.Tree.Entries {
		if snapshot.Tree.Entries[index].Path == snapshot.RecordPath {
			snapshot.Tree.Entries[index].OID = snapshot.RecordBlobOID
		}
	}
}

func TestStateV3PersistsArmsBeforeEveryExternalMutation(t *testing.T) {
	full := fixtureStateV3Record(t)
	for _, operation := range []string{"platform_commit", "tag_object_create", "tag_ref_create"} {
		t.Run(operation, func(t *testing.T) {
			armEvent := -1
			armSeen := 0
			for index, event := range full.Events {
				if event.Kind == StateV3EventMutationArmed {
					if full.MutationArms[armSeen].OperationKind == operation {
						armEvent = index
						break
					}
					armSeen++
				}
			}
			if armEvent < 0 {
				t.Fatal("missing mutation arm")
			}
			crashed := fixtureStateV3RecordAtEventCount(t, full, armEvent+1)
			if err := validateV3Fixture(t, crashed); err != nil {
				t.Fatalf("crash after durable arm rejected: %v", err)
			}
			bypass := cloneStateV3(t, crashed)
			bypass.MutationArms = append(bypass.MutationArms, bypass.MutationArms[len(bypass.MutationArms)-1])
			event := StateV3Event{Kind: StateV3EventMutationArmed, Phase: bypass.Phase, Ref: bypass.MutationArms[len(bypass.MutationArms)-1].Ref, Disposition: "armed"}
			event.EvidenceSHA256, _ = stateV3ExpectedEventEvidenceDigest(bypass, event, len(bypass.Events))
			bypass.Events, _ = appendStateV3Event(bypass.Events, event)
			if err := validateV3Fixture(t, bypass); err == nil {
				t.Fatal("later mutation arm bypassed unresolved attempt")
			}
		})
	}
	refBeforeObjectResult := cloneStateV3(t, full)
	for index := range refBeforeObjectResult.Events {
		if refBeforeObjectResult.Events[index].Kind == StateV3EventTagObjectCreated {
			refBeforeObjectResult.Events = append(refBeforeObjectResult.Events[:index], refBeforeObjectResult.Events[index+1:]...)
			break
		}
	}
	rebindStateV3Events(t, &refBeforeObjectResult)
	if err := validateV3Fixture(t, refBeforeObjectResult); err == nil {
		t.Fatal("tag ref arm accepted before durable tag-object result")
	}
	for name, mutate := range map[string]func(*StateV3Record){
		"arm ref":           func(record *StateV3Record) { record.MutationArms[0].Ref = "refs/heads/caller" },
		"arm intent digest": func(record *StateV3Record) { record.MutationArms[0].IntentSHA256 = v3SHAa },
		"arm expected old": func(record *StateV3Record) {
			record.MutationArms[0].ExpectedOldMissing = true
			record.MutationArms[0].ExpectedOldOID = ""
		},
		"arm attempt": func(record *StateV3Record) { record.MutationArms[0].Attempt = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			record := fixtureStateV3RecordAtEventCount(t, full, 3)
			mutate(&record)
			rebindStateV3Events(t, &record)
			if err := validateV3Fixture(t, record); err == nil {
				t.Fatal("invalid durable mutation arm accepted")
			}
		})
	}
	branch := fixtureStateV3ArmedSourceBranch(t)
	if err := validateV3Fixture(t, branch); err != nil {
		t.Fatalf("crash after branch arm rejected: %v", err)
	}
	branch.MutationArms[0].Attempt = 2
	rebindStateV3Events(t, &branch)
	if err := validateV3Fixture(t, branch); err == nil {
		t.Fatal("non-first durable branch attempt accepted")
	}
}

func TestStateV3RejectsStateMachineInvalidMutationArms(t *testing.T) {
	full := fixtureStateV3Record(t)

	duplicatePlatform := fixtureStateV3RecordAtEventCount(t, full, 4)
	arm := cloneStateV3(t, duplicatePlatform.MutationArms[0])
	duplicatePlatform.MutationArms = append(duplicatePlatform.MutationArms, arm)
	duplicatePlatform, _ = AppendStateV3RecordEvent(duplicatePlatform, StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhasePrepared, Ref: arm.Ref, ExpectedOldOID: arm.ExpectedOldOID, Disposition: "armed"})
	if err := validateV3Fixture(t, duplicatePlatform); err == nil {
		t.Fatal("consumed platform mutation was armed a second time")
	}

	beforeTests := fixtureStateV3RecordAtEventCount(t, full, 6)
	intent := full.TagIntents[0]
	tagArm, _ := stateV3MutationArm("tag_object_create", intent.Ref, "", intent.ExpectedTagObjectOID, true, intent)
	beforeTests.MutationArms = append(beforeTests.MutationArms, tagArm)
	beforeTests, _ = AppendStateV3RecordEvent(beforeTests, StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhaseBranchesPublished, Ref: tagArm.Ref, ObjectOID: tagArm.IntendedObjectOID, Disposition: "armed"})
	if err := validateV3Fixture(t, beforeTests); err == nil {
		t.Fatal("tag object mutation armed before exact-SHA tests passed")
	}

	afterTags := fixtureStateV3RecordAtEventCount(t, full, 13)
	afterTags.MutationArms = append(afterTags.MutationArms, tagArm)
	afterTags, _ = AppendStateV3RecordEvent(afterTags, StateV3Event{Kind: StateV3EventMutationArmed, Phase: StateV3PhaseTagsPublished, Ref: tagArm.Ref, ObjectOID: tagArm.IntendedObjectOID, Disposition: "armed"})
	if err := validateV3Fixture(t, afterTags); err == nil {
		t.Fatal("tag object mutation armed after its result and later phase")
	}
}

func TestStateV3BranchEvidenceBindsResponsesAndIndependentRereads(t *testing.T) {
	armed := fixtureStateV3ArmedSourceBranch(t)
	intent := armed.Prepared.Mutation.Branches[0]
	for name, response := range map[string]StateV3MutationResponse{
		"observed": {Observation: "observed", Attempts: 1, OID: armed.Reservation.SourceOID},
		"lost":     {Observation: "lost", Attempts: 1},
	} {
		t.Run(name, func(t *testing.T) {
			record := cloneStateV3(t, armed)
			record.BranchEvidence = []StateV3BranchEvidence{{Intent: intent, Response: response, ObjectOID: record.Reservation.SourceOID, ObservedRefOID: record.Reservation.SourceOID, Status: "present", Disposition: "published"}}
			event := StateV3Event{Kind: StateV3EventBranchPublished, Phase: StateV3PhasePrepared, Ref: intent.Ref, ObjectOID: record.Reservation.SourceOID, Disposition: "published"}
			record, _ = AppendStateV3RecordEvent(record, event)
			if err := validateV3Fixture(t, record); err != nil {
				t.Fatalf("exact branch evidence rejected: %v", err)
			}
		})
	}
	reconciled := fixtureStateV3ArmedSourceBranch(t)
	removeStateV3MutationArm(t, &reconciled, "branch_ref_create", 0)
	reconciled.BranchEvidence = []StateV3BranchEvidence{{Intent: intent, Response: StateV3MutationResponse{Observation: "not_attempted"}, ObjectOID: reconciled.Reservation.SourceOID, ObservedRefOID: reconciled.Reservation.SourceOID, Status: "present", Disposition: "reconciled"}}
	reconciled, _ = AppendStateV3RecordEvent(reconciled, StateV3Event{Kind: StateV3EventBranchPublished, Phase: StateV3PhasePrepared, Ref: intent.Ref, ObjectOID: reconciled.Reservation.SourceOID, Disposition: "reconciled"})
	if err := validateV3Fixture(t, reconciled); err != nil {
		t.Fatalf("zero-attempt branch reconciliation rejected: %v", err)
	}
	for name, mutate := range map[string]func(*StateV3Record){
		"missing reread":    func(record *StateV3Record) { record.BranchEvidence[0].ObservedRefOID = "" },
		"wrong reread":      func(record *StateV3Record) { record.BranchEvidence[0].ObservedRefOID = v3OIDd },
		"wrong status":      func(record *StateV3Record) { record.BranchEvidence[0].Status = "missing" },
		"wrong disposition": func(record *StateV3Record) { record.BranchEvidence[0].Disposition = "reconciled" },
		"wrong attempt":     func(record *StateV3Record) { record.BranchEvidence[0].Response.Attempts = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneStateV3(t, armed)
			candidate.BranchEvidence = []StateV3BranchEvidence{{Intent: intent, Response: StateV3MutationResponse{Observation: "lost", Attempts: 1}, ObjectOID: candidate.Reservation.SourceOID, ObservedRefOID: candidate.Reservation.SourceOID, Status: "present", Disposition: "published"}}
			candidate, _ = AppendStateV3RecordEvent(candidate, StateV3Event{Kind: StateV3EventBranchPublished, Phase: StateV3PhasePrepared, Ref: intent.Ref, ObjectOID: candidate.Reservation.SourceOID, Disposition: "published"})
			mutate(&candidate)
			rebindStateV3Events(t, &candidate)
			if err := validateV3Fixture(t, candidate); err == nil {
				t.Fatal("invalid branch evidence accepted")
			}
		})
	}
}
