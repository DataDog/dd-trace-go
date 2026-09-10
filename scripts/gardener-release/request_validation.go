// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "context"

// ValidatedDispatch binds a strict dispatch to fresh GitHub comment evidence.
type ValidatedDispatch struct {
	Request         DispatchRequest   `json:"request"`
	Validated       ValidatedRequest  `json:"validated_request"`
	Acknowledgement BoundIssueComment `json:"acknowledgement"`
}

// ValidateDispatchFromGitHub re-fetches both comments before any reservation.
func ValidateDispatchFromGitHub(ctx context.Context, api FeedbackAPI, raw []byte, policy Policy) (ValidatedDispatch, error) {
	if api == nil {
		return ValidatedDispatch{}, newReleaseError(ErrorClassEvidenceIncomplete, "request_api_unavailable")
	}
	request, err := DecodeDispatchJSON(raw)
	if err != nil {
		return ValidatedDispatch{}, err
	}
	original, found, err := api.GetIssueComment(ctx, request.Context.IssueNumber, request.Context.OriginalCommentID)
	if err != nil {
		return ValidatedDispatch{}, wrapReleaseError(ErrorClassEvidenceIncomplete, "original_comment_read_failed", err)
	}
	if !found {
		return ValidatedDispatch{}, typedRequestError("original_comment_missing")
	}
	validated, err := ValidateRequestAgainstPolicy(request, policy, OriginalComment{
		RepositoryID:       policy.RepositoryID,
		RepositoryFullName: original.RepositoryFullName,
		IssueNumber:        original.IssueNumber,
		CommentID:          original.CommentID,
		Body:               original.Body,
		AuthorID:           original.AuthorID,
		AuthorLogin:        original.AuthorLogin,
		AuthorAssociation:  original.AuthorAssociation,
	})
	if err != nil {
		return ValidatedDispatch{}, err
	}
	ack, found, err := api.GetIssueComment(ctx, request.Context.IssueNumber, request.Context.AcknowledgementCommentID)
	if err != nil {
		return ValidatedDispatch{}, wrapReleaseError(ErrorClassEvidenceIncomplete, "acknowledgement_read_failed", err)
	}
	reservation := Reservation{RepositoryFullName: validated.RepositoryFullName, IssueNumber: validated.IssueNumber, AcknowledgementCommentID: validated.AcknowledgementCommentID}
	if !found || !validAcknowledgementFeedbackBinding(ack, reservation, policy.FeedbackIdentity, validated.Marker) {
		return ValidatedDispatch{}, typedRequestError("acknowledgement_binding_changed")
	}
	return ValidatedDispatch{Request: request, Validated: validated, Acknowledgement: ack}, nil
}
