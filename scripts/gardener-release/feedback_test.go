// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeFeedbackAPI struct {
	comments  map[string]BoundIssueComment
	updateErr error
	updates   int
}

func (f *fakeFeedbackAPI) GetIssueComment(_ context.Context, _, id string) (BoundIssueComment, bool, error) {
	comment, ok := f.comments[id]
	return comment, ok, nil
}

func (f *fakeFeedbackAPI) UpdateIssueComment(_ context.Context, id, body string) error {
	f.updates++
	if f.updateErr == nil || f.updateErr.Error() == "accepted_then_lost" {
		comment := f.comments[id]
		comment.Body = body
		f.comments[id] = comment
	}
	return f.updateErr
}

func feedbackFixture() (FeedbackRequest, *fakeFeedbackAPI) {
	reservation := baseReservation()
	reservation.AcknowledgementCommentID = "790"
	marker := Marker(reservation.RepositoryID, reservation.OriginalCommentID, reservation.Command, reservation.RequestedVersion)
	api := &fakeFeedbackAPI{comments: map[string]BoundIssueComment{
		"789": {RepositoryFullName: RepositoryFullName, IssueNumber: reservation.IssueNumber, CommentID: "789", Body: reservation.BodySnapshot, AuthorID: reservation.ValidatedActorID, AuthorLogin: reservation.ValidatedActorLogin, AuthorAssociation: "MEMBER"},
		"790": {RepositoryFullName: RepositoryFullName, IssueNumber: reservation.IssueNumber, CommentID: "790", Body: marker + "\n\nPending", AuthorID: "2001", AuthorLogin: "gardener-bot"},
	}}
	request := FeedbackRequest{Record: Record{Reservation: reservation, Phase: PhaseTagsPublished}, Identity: FeedbackIdentity{GardenerAuthorID: "2001", GardenerAuthorLogin: "gardener-bot"}, State: FeedbackTagsPublished, Version: "v2.9.0", ReleaseSHA: strings.Repeat("a", 40), WorkflowRun: "9001"}
	return request, api
}

func TestFeedbackW05ResponseLossReconcilesCanonicalComment(t *testing.T) {
	request, api := feedbackFixture()
	api.updateErr = errors.New("accepted_then_lost")
	result, err := PublishFeedback(context.Background(), api, request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reconciled || result.CommentID != "790" || api.updates != 1 {
		t.Fatalf("result=%#v updates=%d", result, api.updates)
	}
	if !strings.Contains(api.comments["790"].Body, publicFeedbackMessages[FeedbackTagsPublished]) {
		t.Fatalf("body = %q", api.comments["790"].Body)
	}
}

func TestFeedbackForgedEditedDeletedBindingsNeverMutate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*FeedbackRequest, *fakeFeedbackAPI)
	}{
		{"edited original", func(_ *FeedbackRequest, api *fakeFeedbackAPI) {
			c := api.comments["789"]
			c.Body += " edited"
			api.comments["789"] = c
		}},
		{"deleted original", func(_ *FeedbackRequest, api *fakeFeedbackAPI) { delete(api.comments, "789") }},
		{"forged ack author", func(_ *FeedbackRequest, api *fakeFeedbackAPI) {
			c := api.comments["790"]
			c.AuthorLogin = "attacker"
			api.comments["790"] = c
		}},
		{"prefix collision", func(_ *FeedbackRequest, api *fakeFeedbackAPI) {
			c := api.comments["790"]
			c.Body = strings.Replace(c.Body, " -->", " forged -->", 1)
			api.comments["790"] = c
		}},
		{"duplicate marker", func(_ *FeedbackRequest, api *fakeFeedbackAPI) {
			c := api.comments["790"]
			c.Body += "\n" + strings.Split(c.Body, "\n")[0]
			api.comments["790"] = c
		}},
		{"wrong canonical ID", func(request *FeedbackRequest, _ *fakeFeedbackAPI) {
			request.Record.Reservation.AcknowledgementCommentID = "791"
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request, api := feedbackFixture()
			tc.mutate(&request, api)
			_, err := PublishFeedback(context.Background(), api, request)
			if ClassOf(err) != ErrorClassFeedbackFailed || api.updates != 0 {
				t.Fatalf("error=%q/%q updates=%d", ClassOf(err), ErrorCode(err), api.updates)
			}
		})
	}
}

func TestFeedbackFixedMessagesNeverRenderRawErrors(t *testing.T) {
	request, _ := feedbackFixture()
	request.State = FeedbackImagesFailed
	body, err := RenderPublicFeedback(request, Marker(request.Record.Reservation.RepositoryID, request.Record.Reservation.OriginalCommentID, request.Record.Reservation.Command, request.Record.Reservation.RequestedVersion))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"stderr", "Authorization", "Bearer", request.Record.Reservation.BodySnapshot} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("body contains %q: %s", forbidden, body)
		}
	}
}

func TestFeedbackAuthenticDuplicateKeepsRecordedCanonicalID(t *testing.T) {
	request, _ := feedbackFixture()
	marker := Marker(request.Record.Reservation.RepositoryID, request.Record.Reservation.OriginalCommentID, request.Record.Reservation.Command, request.Record.Reservation.RequestedVersion)
	canonical, err := ReconcileAcknowledgement(request.Record, AcknowledgementCandidate{CommentID: "700", Marker: marker}, marker)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != "790" {
		t.Fatalf("canonical = %s, want recorded 790", canonical)
	}
}
