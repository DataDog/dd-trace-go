// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func semanticSignedOutput(reservation Reservation) SignedOutput {
	source := reservation.SourceRefs[0].SHA
	release := strings.Repeat("4", 40)
	return SignedOutput{
		UnsignedSHA:       strings.Repeat("2", 40),
		SourceSHA:         source,
		TreeSHA:           strings.Repeat("3", 40),
		ReleaseSHA:        release,
		ToolDigest:        strings.Repeat("a", 64),
		ValidatorDigest:   strings.Repeat("b", 64),
		CommitParentSHA:   source,
		SignerFingerprint: strings.Repeat("c", 64),
		ChangedPaths:      []string{"version.go"},
		Tags: []TagRef{{
			Name: reservation.GenerationVersion, Ref: "refs/tags/" + reservation.GenerationVersion,
			TagObjectSHA: strings.Repeat("5", 40), PeeledCommitSHA: release,
		}},
		Bundle: Bundle{Path: "requests/" + reservation.RepositoryID + "/" + reservation.OriginalCommentID + "/recovery.bundle", SHA256: strings.Repeat("d", 64), SizeBytes: 1, PrerequisiteSHAs: []string{source}},
	}
}

func semanticSignedRecord(t *testing.T, reservation Reservation) Record {
	t.Helper()
	decision := sealedReservationDecision(t, reservation)
	return advanceFixtureRecordToPhase(t, decision, semanticSignedOutput(reservation), PhaseSigned)
}

func semanticPromoteRecord(t *testing.T) Record {
	t.Helper()
	reservation := baseReservation()
	decision := sealedReservationDecision(t, reservation)
	record := advanceFixtureRecordToPhase(t, decision, semanticSignedOutput(reservation), PhaseComplete)
	if err := validateLoadedRecord(record); err != nil {
		t.Fatalf("valid promote fixture: %v", err)
	}
	return record
}

func semanticPrepareRecord(t *testing.T) Record {
	t.Helper()
	record := semanticPrepareTagsRecord(t)
	dev := record.SignedOutput.PublicationIntents[1]
	pr := preparePREventEvidence{SchemaVersion: "1", Repository: RepositoryFullName, Command: "release:prepare", Number: "42", URL: "https://github.com/DataDog/dd-trace-go/pull/42", Head: strings.TrimPrefix(dev.Ref, "refs/heads/"), SHA: dev.DesiredSHA, Base: "main", Marker: preparePRMarker(record.Reservation.RequestKey), Disposition: "reconciled"}
	body, err := json.Marshal(pr)
	if err != nil {
		t.Fatal(err)
	}
	record.Events, err = AppendEvent(record.Events, record.Reservation.RequestKey, EventPreparePRRecorded, body)
	if err != nil {
		t.Fatal(err)
	}
	outcome := OperationOutcome{SchemaVersion: "1", Publication: PublicationOutcome{Status: WorkSucceeded, ReleaseSHA: record.SignedOutput.ReleaseSHA}, PreparePR: PreparePROutcome{Status: WorkSucceeded, Number: "42"}, Images: WorkNotApplicable, ImageEvidence: []ImagePromotionEvidence{}}
	record, err = RecordOperationOutcome(record, outcome)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLoadedRecord(record); err != nil {
		t.Fatalf("valid prepare fixture: %v", err)
	}
	return record
}

func semanticPrepareTagsRecord(t *testing.T) Record {
	t.Helper()
	reservation := reservationForSigned("v2.9.0-dev", strings.Repeat("1", 40))
	decision := sealedReservationDecision(t, reservation)
	return advanceFixtureRecordToPhase(t, decision, semanticSignedOutput(reservation), PhaseTagsPublished)
}

func semanticReleaseImageRecord(t *testing.T) Record {
	t.Helper()
	reservation := baseReservation()
	reservation.Command = "release:release"
	reservation.BodySnapshot = "/gardener release:release"
	reservation.ResolvedVersion = "v2.11.0"
	reservation.GenerationVersion = reservation.ResolvedVersion
	reservation.RequestSHA256 = RequestSHA256(Context{RepositoryID: reservation.RepositoryID, RepositoryFullName: reservation.RepositoryFullName, IssueNumber: reservation.IssueNumber, OriginalCommentID: reservation.OriginalCommentID, AcknowledgementCommentID: reservation.AcknowledgementCommentID, BodySnapshot: reservation.BodySnapshot, PolicyRevision: reservation.PolicyRevision}, reservation.Command, reservation.RequestedVersion)
	decision := sealedReservationDecision(t, reservation)
	signed := semanticSignedOutput(reservation)
	signed.Tags = signed.Tags[:1]
	for index, item := range ImagePackagePolicy() {
		signed.Tags = append(signed.Tags, TagRef{Name: item.ModulePrefix + reservation.ResolvedVersion, Ref: "refs/tags/" + item.ModulePrefix + reservation.ResolvedVersion, TagObjectSHA: strings.Repeat(string(rune('6'+index)), 40), PeeledCommitSHA: signed.ReleaseSHA})
	}
	record := advanceFixtureRecordToPhase(t, decision, signed, PhaseTagsPublished)
	first := ImagePackagePolicy()[0]
	moduleTag := record.SignedOutput.Tags[1]
	evidence := ImagePromotionEvidence{SchemaVersion: "1", Outcome: ImagePending, RepositoryFullName: RepositoryFullName, WorkflowPath: ImageWorkflowPath, WorkflowSHA256: strings.Repeat("e", 64), ChildWorkflowSHA256: strings.Repeat("f", 64), RunID: "30", RunAttempt: 1, Event: "push", BuildStatus: "in_progress", ModuleTagRef: moduleTag.Ref, ModuleTagObjectSHA: moduleTag.TagObjectSHA, CommitSHA: record.SignedOutput.ReleaseSHA, Version: reservation.ResolvedVersion, Image: first.Image}
	var err error
	record, err = RecordImageOutcome(record, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLoadedRecord(record); err != nil {
		t.Fatalf("valid release image fixture: %v", err)
	}
	return record
}

func rewriteEvent(t *testing.T, record *Record, kind EventKind, rewrite func(json.RawMessage) json.RawMessage) {
	t.Helper()
	for index := range record.Events {
		if record.Events[index].Kind == kind {
			record.Events[index].Evidence = rewrite(record.Events[index].Evidence)
			rechainSemanticEvents(t, record)
			return
		}
	}
	t.Fatalf("missing event %s", kind)
}

func rechainSemanticEvents(t *testing.T, record *Record) {
	t.Helper()
	previous := ""
	for index := range record.Events {
		record.Events[index].Sequence = index + 1
		record.Events[index].RequestKey = record.Reservation.RequestKey
		record.Events[index].PreviousDigest = previous
		record.Events[index].Digest = ""
		digest, err := computeEventDigest(record.Events[index])
		if err != nil {
			t.Fatal(err)
		}
		record.Events[index].Digest = digest
		previous = digest
	}
}

func mutateRefEvidence(t *testing.T, record *Record, kind EventKind, mutate func(*refEventEvidence)) {
	t.Helper()
	rewriteEvent(t, record, kind, func(raw json.RawMessage) json.RawMessage {
		var evidence refEventEvidence
		if err := json.Unmarshal(raw, &evidence); err != nil {
			t.Fatal(err)
		}
		mutate(&evidence)
		return mustJSON(t, evidence)
	})
}

func mutateTestEvidence(t *testing.T, record *Record, mutate func(*testEventEvidence)) {
	t.Helper()
	rewriteEvent(t, record, EventTestsPassed, func(raw json.RawMessage) json.RawMessage {
		var evidence testEventEvidence
		if err := json.Unmarshal(raw, &evidence); err != nil {
			t.Fatal(err)
		}
		mutate(&evidence)
		canonical, err := canonicalJSON(evidence.Detail)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(canonical)
		evidence.Evidence.EvidenceSHA256 = hex.EncodeToString(digest[:])
		return mustJSON(t, evidence)
	})
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func insertDuplicateEventBeforeComplete(t *testing.T, record *Record, kind EventKind) {
	t.Helper()
	duplicateIndex, completeIndex := -1, -1
	for index, event := range record.Events {
		if event.Kind == kind && duplicateIndex < 0 {
			duplicateIndex = index
		}
		if event.Kind == EventPhaseAdvanced {
			var evidence phaseEventEvidence
			if json.Unmarshal(event.Evidence, &evidence) == nil && evidence.Phase == PhaseComplete {
				completeIndex = index
			}
		}
	}
	if duplicateIndex < 0 || completeIndex < 0 {
		t.Fatalf("missing %s or complete event", kind)
	}
	duplicate := record.Events[duplicateIndex]
	record.Events = append(record.Events, Event{})
	copy(record.Events[completeIndex+1:], record.Events[completeIndex:])
	record.Events[completeIndex] = duplicate
}

func assertAuthenticatedStateLoadRejected(t *testing.T, record Record) {
	t.Helper()
	rechainSemanticEvents(t, &record)
	remote := newBareFixtureRemote(t)
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	seedStateBranch(t, remote, StateBranch, map[string]string{".keep": "state\n"})
	store := newFixtureStore(t, remote, signer)
	loaded, err := store.LoadState(context.Background(), record.Reservation.RequestKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PersistReservation(context.Background(), ReservationDecision{Reserved: true, Record: record}, loaded.RemoteHead); err != nil {
		t.Fatal(err)
	}
	if loaded, err = store.LoadState(context.Background(), record.Reservation.RequestKey); err == nil || loaded.Found {
		t.Fatalf("semantically invalid authenticated state loaded: %#v", loaded.Record)
	}
}

func TestGitStateStoreRejectsAuthenticatedSemanticMutationMatrix(t *testing.T) {
	type mutation struct {
		name   string
		base   func(*testing.T) Record
		mutate func(*testing.T, *Record)
	}
	promote := semanticPromoteRecord
	prepare := semanticPrepareRecord
	release := semanticReleaseImageRecord
	cases := []mutation{
		{"reservation decimal ID", promote, func(_ *testing.T, r *Record) { r.Reservation.AcknowledgementCommentID = "01" }},
		{"reservation actor ID", promote, func(_ *testing.T, r *Record) { r.Reservation.ValidatedActorID = "actor" }},
		{"reservation actor login", promote, func(_ *testing.T, r *Record) { r.Reservation.ValidatedActorLogin = " actor " }},
		{"reservation request digest", promote, func(_ *testing.T, r *Record) { r.Reservation.RequestSHA256 = strings.Repeat("0", 64) }},
		{"reservation source SHA", promote, func(_ *testing.T, r *Record) { r.Reservation.SourceRefs[0].SHA = "bad" }},
		{"reservation policy digest", promote, func(_ *testing.T, r *Record) { r.Reservation.PolicyRevision = strings.Repeat("A", 64) }},
		{"reservation version command", promote, func(_ *testing.T, r *Record) { r.Reservation.ResolvedVersion = "v2.11.0" }},
		{"reservation source ref", promote, func(_ *testing.T, r *Record) { r.Reservation.SourceRefs[0].Ref = "refs/heads/main" }},
		{"workflow SHA", promote, func(_ *testing.T, r *Record) { r.WorkflowSHA = "bad" }},
		{"tool SHA", promote, func(_ *testing.T, r *Record) { r.ToolSHA = "bad" }},
		{"signed tool digest", promote, func(_ *testing.T, r *Record) { r.SignedOutput.ToolDigest = "bad" }},
		{"signed signer fingerprint", promote, func(_ *testing.T, r *Record) { r.SignedOutput.SignerFingerprint = "bad" }},
		{"signing intent fingerprint mismatch", promote, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventSigningIntent, func(raw json.RawMessage) json.RawMessage {
				var intent GitSigningIntent
				if err := json.Unmarshal(raw, &intent); err != nil {
					t.Fatal(err)
				}
				intent.Fingerprint = fixtureOpenSSHFingerprint(t, strings.Repeat("9", 64))
				return mustJSON(t, intent)
			})
		}},
		{"signed publication intent", promote, func(_ *testing.T, r *Record) {
			r.SignedOutput.PublicationIntents[0].DesiredSHA = strings.Repeat("9", 40)
		}},
		{"signed bundle path", promote, func(_ *testing.T, r *Record) { r.SignedOutput.Bundle.Path = "requests/123/999/recovery.bundle" }},
		{"branch disposition", promote, func(t *testing.T, r *Record) {
			mutateRefEvidence(t, r, EventBranchPublished, func(e *refEventEvidence) { e.Disposition = "forced" })
		}},
		{"branch unrelated ref", promote, func(t *testing.T, r *Record) {
			mutateRefEvidence(t, r, EventBranchPublished, func(e *refEventEvidence) { e.Ref = "refs/heads/release-v9.9.x" })
		}},
		{"branch unrelated object", promote, func(t *testing.T, r *Record) {
			mutateRefEvidence(t, r, EventBranchPublished, func(e *refEventEvidence) { e.ObjectSHA = strings.Repeat("9", 40) })
		}},
		{"branch expected old SHA", promote, func(t *testing.T, r *Record) {
			mutateRefEvidence(t, r, EventBranchPublished, func(e *refEventEvidence) { e.ExpectedOldSHA = strings.Repeat("9", 40) })
		}},
		{"tag disposition", promote, func(t *testing.T, r *Record) {
			mutateRefEvidence(t, r, EventTagPublished, func(e *refEventEvidence) { e.Disposition = "updated" })
		}},
		{"tag unrelated ref", promote, func(t *testing.T, r *Record) {
			mutateRefEvidence(t, r, EventTagPublished, func(e *refEventEvidence) { e.Ref = "refs/tags/v9.9.9" })
		}},
		{"tag unrelated object", promote, func(t *testing.T, r *Record) {
			mutateRefEvidence(t, r, EventTagPublished, func(e *refEventEvidence) { e.ObjectSHA = strings.Repeat("9", 40) })
		}},
		{"test run ID", promote, func(t *testing.T, r *Record) {
			mutateTestEvidence(t, r, func(e *testEventEvidence) { e.Detail.Runs[0].RunID = "01" })
		}},
		{"test target release SHA", promote, func(t *testing.T, r *Record) {
			mutateTestEvidence(t, r, func(e *testEventEvidence) { e.Detail.Runs[0].TargetSHA = strings.Repeat("9", 40) })
		}},
		{"test run status", promote, func(t *testing.T, r *Record) {
			mutateTestEvidence(t, r, func(e *testEventEvidence) { e.Detail.Runs[0].Status = "in_progress" })
		}},
		{"test run conclusion", promote, func(t *testing.T, r *Record) {
			mutateTestEvidence(t, r, func(e *testEventEvidence) { e.Detail.Runs[0].Conclusion = "cancelled" })
		}},
		{"test job ID", promote, func(t *testing.T, r *Record) {
			mutateTestEvidence(t, r, func(e *testEventEvidence) { e.Detail.Runs[0].Jobs[0].ID = "bad" })
		}},
		{"test job status", promote, func(t *testing.T, r *Record) {
			mutateTestEvidence(t, r, func(e *testEventEvidence) { e.Detail.Runs[0].Jobs[0].Status = "queued" })
		}},
		{"test job conclusion", promote, func(t *testing.T, r *Record) {
			mutateTestEvidence(t, r, func(e *testEventEvidence) { e.Detail.Runs[0].Jobs[0].Conclusion = "failure" })
		}},
		{"test evidence release SHA", promote, func(t *testing.T, r *Record) {
			mutateTestEvidence(t, r, func(e *testEventEvidence) { e.Evidence.ReleaseSHA = strings.Repeat("9", 40) })
		}},
		{"test development SHA", prepare, func(t *testing.T, r *Record) {
			mutateTestEvidence(t, r, func(e *testEventEvidence) { e.Detail.Runs[1].TargetSHA = strings.Repeat("9", 40) })
		}},
		{"PR command", prepare, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventPreparePRRecorded, func(raw json.RawMessage) json.RawMessage {
				var e preparePREventEvidence
				_ = json.Unmarshal(raw, &e)
				e.Command = "release:release"
				return mustJSON(t, e)
			})
		}},
		{"PR head", prepare, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventPreparePRRecorded, func(raw json.RawMessage) json.RawMessage {
				var e preparePREventEvidence
				_ = json.Unmarshal(raw, &e)
				e.Head = "release-v9.9.x"
				return mustJSON(t, e)
			})
		}},
		{"PR base", prepare, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventPreparePRRecorded, func(raw json.RawMessage) json.RawMessage {
				var e preparePREventEvidence
				_ = json.Unmarshal(raw, &e)
				e.Base = "release-v2.8.x"
				return mustJSON(t, e)
			})
		}},
		{"PR URL", prepare, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventPreparePRRecorded, func(raw json.RawMessage) json.RawMessage {
				var e preparePREventEvidence
				_ = json.Unmarshal(raw, &e)
				e.URL = "https://example.invalid/pull/42"
				return mustJSON(t, e)
			})
		}},
		{"image workflow digest", release, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventImageObserved, func(raw json.RawMessage) json.RawMessage {
				var e ImagePromotionEvidence
				_ = json.Unmarshal(raw, &e)
				e.WorkflowSHA256 = "bad"
				return mustJSON(t, e)
			})
		}},
		{"image child workflow digest", release, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventImageObserved, func(raw json.RawMessage) json.RawMessage {
				var e ImagePromotionEvidence
				_ = json.Unmarshal(raw, &e)
				e.ChildWorkflowSHA256 = "bad"
				return mustJSON(t, e)
			})
		}},
		{"image outcome enum", release, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventImageObserved, func(raw json.RawMessage) json.RawMessage {
				var e ImagePromotionEvidence
				_ = json.Unmarshal(raw, &e)
				e.Outcome = "unknown"
				return mustJSON(t, e)
			})
		}},
		{"image package", release, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventImageObserved, func(raw json.RawMessage) json.RawMessage {
				var e ImagePromotionEvidence
				_ = json.Unmarshal(raw, &e)
				e.Image = "ghcr.io/attacker/package"
				return mustJSON(t, e)
			})
		}},
		{"image tag", release, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventImageObserved, func(raw json.RawMessage) json.RawMessage {
				var e ImagePromotionEvidence
				_ = json.Unmarshal(raw, &e)
				e.ModuleTagRef = "refs/tags/v2.11.0"
				return mustJSON(t, e)
			})
		}},
		{"image run", release, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventImageObserved, func(raw json.RawMessage) json.RawMessage {
				var e ImagePromotionEvidence
				_ = json.Unmarshal(raw, &e)
				e.RunID = "01"
				return mustJSON(t, e)
			})
		}},
		{"image status", release, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventImageObserved, func(raw json.RawMessage) json.RawMessage {
				var e ImagePromotionEvidence
				_ = json.Unmarshal(raw, &e)
				e.BuildStatus = "unknown"
				return mustJSON(t, e)
			})
		}},
		{"image conclusion", release, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventImageObserved, func(raw json.RawMessage) json.RawMessage {
				var e ImagePromotionEvidence
				_ = json.Unmarshal(raw, &e)
				e.BuildConclusion = "success"
				return mustJSON(t, e)
			})
		}},
		{"outcome enum", promote, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventOutcomeRecorded, func(raw json.RawMessage) json.RawMessage {
				var e OperationOutcome
				_ = json.Unmarshal(raw, &e)
				e.Publication.Status = "unknown"
				return mustJSON(t, e)
			})
		}},
		{"outcome release SHA", promote, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventOutcomeRecorded, func(raw json.RawMessage) json.RawMessage {
				var e OperationOutcome
				_ = json.Unmarshal(raw, &e)
				e.Publication.ReleaseSHA = strings.Repeat("9", 40)
				return mustJSON(t, e)
			})
		}},
		{"premature prepare outcome", semanticPrepareTagsRecord, func(t *testing.T, r *Record) {
			outcome := OperationOutcome{SchemaVersion: "1", Publication: PublicationOutcome{Status: WorkSucceeded, ReleaseSHA: r.SignedOutput.ReleaseSHA}, PreparePR: PreparePROutcome{Status: WorkSucceeded, Number: "42"}, Images: WorkNotApplicable, ImageEvidence: []ImagePromotionEvidence{}}
			body := mustJSON(t, outcome)
			var err error
			r.Events, err = AppendEvent(r.Events, r.Reservation.RequestKey, EventOutcomeRecorded, body)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"future PR evidence cannot authorize earlier outcome", prepare, func(t *testing.T, r *Record) {
			prIndex, outcomeIndex := -1, -1
			for index, event := range r.Events {
				switch event.Kind {
				case EventPreparePRRecorded:
					prIndex = index
				case EventOutcomeRecorded:
					outcomeIndex = index
				}
			}
			if prIndex < 0 || outcomeIndex < 0 {
				t.Fatal("missing prepare events")
			}
			r.Events[prIndex], r.Events[outcomeIndex] = r.Events[outcomeIndex], r.Events[prIndex]
			// Preserve a production-shaped terminal pair after the future PR so
			// this would pass a non-causal validator that let future evidence
			// authorize the first outcome and tolerated idempotent duplicates.
			insertDuplicateEventBeforeComplete(t, r, EventOutcomeRecorded)
		}},
		{"duplicate outcome evidence", prepare, func(t *testing.T, r *Record) {
			insertDuplicateEventBeforeComplete(t, r, EventOutcomeRecorded)
		}},
		{"duplicate image evidence", release, func(t *testing.T, r *Record) {
			var duplicate json.RawMessage
			for _, event := range r.Events {
				if event.Kind == EventImageObserved {
					duplicate = append(json.RawMessage(nil), event.Evidence...)
					break
				}
			}
			if duplicate == nil {
				t.Fatal("missing image event")
			}
			var err error
			r.Events, err = AppendEvent(r.Events, r.Reservation.RequestKey, EventImageObserved, duplicate)
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"legacy feedback evidence", promote, func(t *testing.T, r *Record) {
			var err error
			r.Events, err = AppendEvent(r.Events, r.Reservation.RequestKey, EventAcknowledgementBound, json.RawMessage(`{"comment_id":"790"}`))
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"legacy failure evidence", promote, func(t *testing.T, r *Record) {
			var err error
			r.Events, err = AppendEvent(r.Events, r.Reservation.RequestKey, EventFailureObserved, json.RawMessage(`{"request_key":"123:789","error":"free form"}`))
			if err != nil {
				t.Fatal(err)
			}
		}},
		{"illegal phase skip", promote, func(t *testing.T, r *Record) {
			rewriteEvent(t, r, EventPhaseAdvanced, func(raw json.RawMessage) json.RawMessage {
				var e phaseEventEvidence
				_ = json.Unmarshal(raw, &e)
				e.Phase = PhaseTestsPassed
				return mustJSON(t, e)
			})
		}},
		{"duplicate phase", promote, func(t *testing.T, r *Record) {
			body := mustJSON(t, phaseEventEvidence{Phase: PhaseComplete})
			var err error
			r.Events, err = AppendEvent(r.Events, r.Reservation.RequestKey, EventPhaseAdvanced, body)
			if err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			record := test.base(t)
			test.mutate(t, &record)
			assertAuthenticatedStateLoadRejected(t, record)
		})
	}
}
