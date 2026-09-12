// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"strings"
	"testing"
)

func TestSelectOperationResumePhasesW02W03(t *testing.T) {
	request := ValidatedRequest{RequestKey: "123:789", RequestSHA256: strings.Repeat("a", 64), Command: "release:promote", PolicyRevision: strings.Repeat("b", 64)}
	record := Record{Reservation: baseReservation()}
	record.Reservation.RequestKey = request.RequestKey
	record.Reservation.RequestSHA256 = request.RequestSHA256
	record.Reservation.Command = request.Command
	record.Reservation.PolicyRevision = request.PolicyRevision
	head := strings.Repeat("c", 40)
	cases := []struct {
		phase OperationPhase
		next  OrchestrationJob
	}{
		{PhaseReserved, JobGenerateUnsigned},
		{PhaseSigned, JobPublishBranches},
		{PhaseBranchesPublished, JobWaitBranchTests},
		{PhaseTestsPassed, JobPublishTags},
		{PhaseTagsPublished, JobRecordOutcome},
		{PhaseComplete, JobFeedback},
	}
	for _, test := range cases {
		record.Phase = test.phase
		selector, err := SelectOperation(request, LoadResult{Found: true, RemoteHead: head, Record: record})
		if err != nil {
			t.Fatalf("phase %s: %v", test.phase, err)
		}
		if selector.NextJob != test.next || selector.StateHead != head || !selector.RecordFound {
			t.Fatalf("phase %s selector = %#v", test.phase, selector)
		}
	}
}

func TestValidateOperationSelectorRejectsChangedDecision(t *testing.T) {
	request := ValidatedRequest{RequestKey: "123:789", RequestSHA256: strings.Repeat("a", 64), Command: "release:prepare", PolicyRevision: strings.Repeat("b", 64)}
	selector, err := SelectOperation(request, LoadResult{RemoteHead: strings.Repeat("c", 40)})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationSelector(request, selector); err != nil {
		t.Fatal(err)
	}
	selector.NextJob = JobPublishTags
	if err := ValidateOperationSelector(request, selector); err == nil {
		t.Fatal("changed next job accepted")
	}
}

func TestFeedbackRecordForUnreservedRequestBindsValidatedComments(t *testing.T) {
	request := DispatchRequest{
		Command:       "release:prepare",
		Version:       "v2.3",
		RequestKey:    "123:789",
		RequestSHA256: strings.Repeat("a", 64),
		Context: Context{
			RepositoryID:             "123",
			RepositoryFullName:       "DataDog/dd-trace-go",
			IssueNumber:              "456",
			OriginalCommentID:        "789",
			AcknowledgementCommentID: "790",
			BodySnapshot:             "/gardener release:prepare v2.3",
			PolicyRevision:           strings.Repeat("b", 64),
		},
	}
	validated := ValidatedRequest{
		RequestKey: request.RequestKey, RequestSHA256: request.RequestSHA256, Command: request.Command, Version: request.Version,
		ReleaseLine: "2.3", RepositoryID: request.Context.RepositoryID, RepositoryFullName: request.Context.RepositoryFullName,
		IssueNumber: request.Context.IssueNumber, OriginalCommentID: request.Context.OriginalCommentID,
		AcknowledgementCommentID: request.Context.AcknowledgementCommentID, PolicyRevision: request.Context.PolicyRevision,
		ValidatedActorID: "321", ValidatedActorLogin: "operator",
	}
	record, err := FeedbackRecordForUnreservedRequest(request, validated)
	if err != nil {
		t.Fatal(err)
	}
	if record.Reservation.BodySnapshot != request.Context.BodySnapshot || record.Reservation.ValidatedActorID != "321" || record.Phase != "" {
		t.Fatalf("unexpected feedback-only record: %#v", record)
	}
	changed := validated
	changed.RequestSHA256 = strings.Repeat("c", 64)
	if _, err := FeedbackRecordForUnreservedRequest(request, changed); err == nil {
		t.Fatal("changed validated request accepted")
	}
}

func TestSelectOperationNoRecordAndBindingConflictW06W07(t *testing.T) {
	request := ValidatedRequest{RequestKey: "123:789", RequestSHA256: strings.Repeat("a", 64), Command: "release:prepare", PolicyRevision: strings.Repeat("b", 64)}
	head := strings.Repeat("c", 40)
	selector, err := SelectOperation(request, LoadResult{RemoteHead: head})
	if err != nil || selector.NextJob != JobReserveOperation || selector.RecordFound {
		t.Fatalf("no-record selector = %#v, %v", selector, err)
	}
	record := Record{Reservation: baseReservation()}
	record.Reservation.RequestKey = request.RequestKey
	record.Reservation.RequestSHA256 = strings.Repeat("d", 64)
	record.Reservation.Command = request.Command
	record.Reservation.PolicyRevision = request.PolicyRevision
	if _, err := SelectOperation(request, LoadResult{Found: true, RemoteHead: head, Record: record}); err == nil {
		t.Fatal("changed request hash accepted")
	}
}
