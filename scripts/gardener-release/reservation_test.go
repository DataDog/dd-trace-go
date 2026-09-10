// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"strings"
	"testing"
	"time"
)

func TestBuildReservationPrepareRecordsOneNextDevelopmentGeneration(t *testing.T) {
	requestContext := Context{RepositoryID: "123", RepositoryFullName: RepositoryFullName, IssueNumber: "456", OriginalCommentID: "789", AcknowledgementCommentID: "790", BodySnapshot: "/gardener release:prepare v2.9", PolicyRevision: strings.Repeat("2", 64)}
	request := DispatchRequest{ContractVersion: ContractVersion, Command: "release:prepare", Version: "v2.9.0", Context: requestContext, RequestKey: "123:789", RequestSHA256: RequestSHA256(requestContext, "release:prepare", "v2.9.0")}
	validated := ValidatedRequest{RequestKey: request.RequestKey, RequestSHA256: request.RequestSHA256, Command: request.Command, Version: request.Version, ReleaseLine: "v2.9", RepositoryID: "123", RepositoryFullName: RepositoryFullName, IssueNumber: "456", OriginalCommentID: "789", AcknowledgementCommentID: "790", PolicyRevision: requestContext.PolicyRevision, ValidatedActorID: "42", ValidatedActorLogin: "maintainer"}
	dispatch := ValidatedDispatch{Request: request, Validated: validated}
	resolution := VersionResolution{Command: "release:prepare", ReleaseLine: "v2.9", ResolvedVersion: "v2.9.0", DevelopmentVersion: "v2.10.0-dev", ReleaseBranch: "release-v2.9.x", DevelopmentBranch: "dev-v2.10.x"}
	reservation, err := BuildReservation(dispatch, resolution, SourceRef{Ref: "refs/heads/main", SHA: strings.Repeat("a", 40)}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if reservation.GenerationVersion != "v2.10.0-dev" || reservation.ResolvedVersion != "v2.9.0" || len(reservation.BranchIntents) != 2 || reservation.BranchIntents[0].DesiredSHA != "pending" || reservation.BranchIntents[1].DesiredSHA != "pending" {
		t.Fatalf("reservation = %#v", reservation)
	}
}
