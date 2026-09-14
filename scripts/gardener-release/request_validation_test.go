// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"testing"
)

func TestValidateDispatchFromGitHubBindsAPIDerivedActorAndAcknowledgement(t *testing.T) {
	policyRaw := validPolicyJSON()
	policy, err := DecodePolicy(policyRaw)
	if err != nil {
		t.Fatal(err)
	}
	request, err := DecodeDispatchJSON(validDispatchJSON(policyRaw))
	if err != nil {
		t.Fatal(err)
	}
	api := &fakeFeedbackAPI{comments: map[string]BoundIssueComment{
		"789": {RepositoryFullName: RepositoryFullName, IssueNumber: "456", CommentID: "789", Body: request.Context.BodySnapshot, AuthorID: "42", AuthorLogin: "maintainer", AuthorAssociation: "MEMBER"},
		"790": {RepositoryFullName: RepositoryFullName, IssueNumber: "456", CommentID: "790", Body: request.Marker, AuthorID: policy.FeedbackIdentity.GardenerAuthorID, AuthorLogin: policy.FeedbackIdentity.GardenerAuthorLogin},
	}}
	result, err := ValidateDispatchFromGitHub(context.Background(), api, validDispatchJSON(policyRaw), policy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Validated.ValidatedActorID != "42" || result.Validated.ValidatedActorLogin != "maintainer" {
		t.Fatalf("actor = %#v", result.Validated)
	}
	api.comments["789"] = BoundIssueComment{RepositoryFullName: RepositoryFullName, IssueNumber: "456", CommentID: "789", Body: request.Context.BodySnapshot, AuthorID: "43", AuthorLogin: "forged", AuthorAssociation: "CONTRIBUTOR"}
	if _, err := ValidateDispatchFromGitHub(context.Background(), api, validDispatchJSON(policyRaw), policy); err == nil {
		t.Fatal("forged direct dispatch accepted")
	}
	if api.updates != 0 {
		t.Fatalf("updates = %d", api.updates)
	}
}
