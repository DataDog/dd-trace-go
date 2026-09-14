// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"fmt"
	"strings"
)

// FeedbackState is the fixed public outcome vocabulary. Arbitrary internal
// errors never become comment text.
type FeedbackState string

const (
	FeedbackRejected           FeedbackState = "rejected"
	FeedbackPending            FeedbackState = "pending"
	FeedbackTesting            FeedbackState = "testing"
	FeedbackPartialPublication FeedbackState = "partial_publication"
	FeedbackTagsPublished      FeedbackState = "tags_published"
	FeedbackImagesPending      FeedbackState = "images_pending"
	FeedbackImagesFailed       FeedbackState = "images_failed"
	FeedbackComplete           FeedbackState = "complete"
	FeedbackFailed             FeedbackState = "feedback_failed"
)

var publicFeedbackMessages = map[FeedbackState]string{
	FeedbackRejected:           "Release request rejected. No release refs were published.",
	FeedbackPending:            "Release request accepted and pending serialized processing.",
	FeedbackTesting:            "Release branches were published; exact-SHA tests are pending.",
	FeedbackPartialPublication: "Release publication is partial. An operator must reconcile the recorded operation.",
	FeedbackTagsPublished:      "All recorded release tags were published.",
	FeedbackImagesPending:      "Release tags were published; versioned images are still pending.",
	FeedbackImagesFailed:       "Release tags were published, but one or more versioned images failed.",
	FeedbackComplete:           "Release operation completed and all recorded outcomes were verified.",
	FeedbackFailed:             "Release outcome was recorded, but feedback delivery requires reconciliation.",
}

// BoundIssueComment is populated from a GitHub API response. Repository and
// issue are response-derived binding data, never copied from dispatch input.
type BoundIssueComment struct {
	RepositoryFullName string
	IssueNumber        string
	CommentID          string
	Body               string
	AuthorID           string
	AuthorLogin        string
	AuthorAssociation  string
}

// FeedbackIdentity is reviewed non-secret policy data for the Gardener bot.
type FeedbackIdentity struct {
	GardenerAuthorID    string
	GardenerAuthorLogin string
}

// FeedbackAPI is intentionally narrow: feedback can read and edit issue
// comments but has no contents, branch, tag, PR, signing, or token-mint seam.
type FeedbackAPI interface {
	GetIssueComment(context.Context, string, string) (BoundIssueComment, bool, error)
	UpdateIssueComment(context.Context, string, string) error
}

// FeedbackRequest contains only validated public outcome fields. Raw errors,
// API payloads, and dispatch text are intentionally absent.
type FeedbackRequest struct {
	Record      Record
	Identity    FeedbackIdentity
	State       FeedbackState
	Version     string
	ReleaseSHA  string
	PRNumber    string
	WorkflowRun string
}

type FeedbackResult struct {
	CommentID  string        `json:"comment_id"`
	State      FeedbackState `json:"state"`
	Reconciled bool          `json:"reconciled"`
}

// PublishFeedback re-fetches and binds the original request and canonical
// acknowledgement immediately before editing. A mutation response is never
// trusted: success and failure are both followed by an exact body read.
func PublishFeedback(ctx context.Context, api FeedbackAPI, request FeedbackRequest) (FeedbackResult, error) {
	if api == nil {
		return FeedbackResult{}, newReleaseError(ErrorClassFeedbackFailed, "feedback_api_unavailable")
	}
	reservation := request.Record.Reservation
	if !validRecordRepositoryBinding(reservation) || reservation.OriginalCommentID == "" || reservation.AcknowledgementCommentID == "" {
		return FeedbackResult{}, newReleaseError(ErrorClassFeedbackFailed, "feedback_record_invalid")
	}
	original, found, err := api.GetIssueComment(ctx, reservation.IssueNumber, reservation.OriginalCommentID)
	if err != nil {
		return FeedbackResult{}, wrapReleaseError(ErrorClassFeedbackFailed, "feedback_original_read_failed", err)
	}
	if !found || !validOriginalFeedbackBinding(original, reservation) {
		return FeedbackResult{}, newReleaseError(ErrorClassFeedbackFailed, "feedback_original_binding_changed")
	}
	ack, found, err := api.GetIssueComment(ctx, reservation.IssueNumber, reservation.AcknowledgementCommentID)
	if err != nil {
		return FeedbackResult{}, wrapReleaseError(ErrorClassFeedbackFailed, "feedback_acknowledgement_read_failed", err)
	}
	expectedMarker := Marker(reservation.RepositoryID, reservation.OriginalCommentID, reservation.Command, reservation.RequestedVersion)
	if !found || !validAcknowledgementFeedbackBinding(ack, reservation, request.Identity, expectedMarker) {
		return FeedbackResult{}, newReleaseError(ErrorClassFeedbackFailed, "feedback_acknowledgement_binding_changed")
	}
	body, err := RenderPublicFeedback(request, expectedMarker)
	if err != nil {
		return FeedbackResult{}, err
	}
	mutationErr := api.UpdateIssueComment(ctx, reservation.AcknowledgementCommentID, body)
	post, postFound, readErr := api.GetIssueComment(ctx, reservation.IssueNumber, reservation.AcknowledgementCommentID)
	if readErr != nil {
		return FeedbackResult{}, wrapReleaseError(ErrorClassFeedbackFailed, "feedback_reconcile_read_failed", readErr)
	}
	if postFound && validAcknowledgementFeedbackBinding(post, reservation, request.Identity, expectedMarker) && post.Body == body {
		return FeedbackResult{CommentID: reservation.AcknowledgementCommentID, State: request.State, Reconciled: mutationErr != nil}, nil
	}
	if mutationErr != nil {
		return FeedbackResult{}, wrapReleaseError(ErrorClassFeedbackFailed, "feedback_update_failed", mutationErr)
	}
	return FeedbackResult{}, newReleaseError(ErrorClassFeedbackFailed, "feedback_update_not_reconciled")
}

func validOriginalFeedbackBinding(comment BoundIssueComment, reservation Reservation) bool {
	return comment.RepositoryFullName == reservation.RepositoryFullName && comment.IssueNumber == reservation.IssueNumber &&
		comment.CommentID == reservation.OriginalCommentID && comment.Body == reservation.BodySnapshot &&
		comment.AuthorID == reservation.ValidatedActorID && comment.AuthorLogin == reservation.ValidatedActorLogin &&
		authorizedAssociation(comment.AuthorAssociation)
}

func validAcknowledgementFeedbackBinding(comment BoundIssueComment, reservation Reservation, identity FeedbackIdentity, marker string) bool {
	return validID(identity.GardenerAuthorID) && identity.GardenerAuthorLogin != "" &&
		comment.RepositoryFullName == reservation.RepositoryFullName && comment.IssueNumber == reservation.IssueNumber &&
		comment.CommentID == reservation.AcknowledgementCommentID && comment.AuthorID == identity.GardenerAuthorID &&
		comment.AuthorLogin == identity.GardenerAuthorLogin && hasExactMarker(comment.Body, marker)
}

func hasExactMarker(body, marker string) bool {
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == marker {
			count++
		}
	}
	return count == 1
}

// RenderPublicFeedback emits only fixed prose and separately validated public
// identifiers. It never accepts a free-form error or message.
func RenderPublicFeedback(request FeedbackRequest, marker string) (string, error) {
	message, ok := publicFeedbackMessages[request.State]
	if !ok || !hasValidMarkerSyntax(marker) {
		return "", newReleaseError(ErrorClassFeedbackFailed, "invalid_feedback_state")
	}
	lines := []string{marker, "", message}
	if request.Version != "" {
		if _, err := parseImageVersion(request.Version); err != nil {
			return "", newReleaseError(ErrorClassFeedbackFailed, "invalid_feedback_version")
		}
		lines = append(lines, "", "Version: `"+request.Version+"`")
	}
	if request.ReleaseSHA != "" {
		if !ValidGitObjectID(request.ReleaseSHA) {
			return "", newReleaseError(ErrorClassFeedbackFailed, "invalid_feedback_sha")
		}
		lines = append(lines, fmt.Sprintf("Commit: https://github.com/DataDog/dd-trace-go/commit/%s", request.ReleaseSHA))
	}
	if request.PRNumber != "" {
		if !validID(request.PRNumber) {
			return "", newReleaseError(ErrorClassFeedbackFailed, "invalid_feedback_pr")
		}
		lines = append(lines, "Pull request: https://github.com/DataDog/dd-trace-go/pull/"+request.PRNumber)
	}
	if request.WorkflowRun != "" {
		if !validID(request.WorkflowRun) {
			return "", newReleaseError(ErrorClassFeedbackFailed, "invalid_feedback_run")
		}
		lines = append(lines, "Workflow: https://github.com/DataDog/dd-trace-go/actions/runs/"+request.WorkflowRun)
	}
	return strings.Join(lines, "\n"), nil
}

func hasValidMarkerSyntax(marker string) bool {
	return strings.HasPrefix(marker, "<!-- gardener:release:request:v1:") && strings.HasSuffix(marker, " -->") && !strings.ContainsAny(marker, "\r\n")
}
