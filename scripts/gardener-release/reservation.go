// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "time"

// BuildReservation fixes the source, version, and pending branch identities
// before generation. The signed transition resolves only each desired SHA.
func BuildReservation(dispatch ValidatedDispatch, resolution VersionResolution, source SourceRef, createdAt time.Time) (Reservation, error) {
	validated := dispatch.Validated
	request := dispatch.Request
	if resolution.Resume || resolution.Command != validated.Command || resolution.ReleaseLine != validated.ReleaseLine || !ValidGitObjectID(source.SHA) || createdAt.IsZero() {
		return Reservation{}, newReleaseError(ErrorClassStateConflict, "invalid_reservation_evidence")
	}
	reservation := Reservation{
		SchemaVersion:            StateSchemaVersion,
		RequestKey:               validated.RequestKey,
		RequestSHA256:            validated.RequestSHA256,
		RepositoryID:             validated.RepositoryID,
		RepositoryFullName:       validated.RepositoryFullName,
		IssueNumber:              validated.IssueNumber,
		OriginalCommentID:        validated.OriginalCommentID,
		AcknowledgementCommentID: validated.AcknowledgementCommentID,
		Command:                  validated.Command,
		RequestedVersion:         validated.Version,
		BodySnapshot:             request.Context.BodySnapshot,
		ValidatedActorID:         validated.ValidatedActorID,
		ValidatedActorLogin:      validated.ValidatedActorLogin,
		PolicyRevision:           validated.PolicyRevision,
		ResolvedVersion:          resolution.ResolvedVersion,
		DevelopmentVersion:       resolution.DevelopmentVersion,
		ReleaseLine:              validated.ReleaseLine,
		SourceRefs:               []SourceRef{source},
		CreatedAt:                createdAt.UTC().Format(time.RFC3339),
	}
	switch validated.Command {
	case "release:prepare":
		if source.Ref != "refs/heads/main" || resolution.ReleaseBranch == "" || resolution.DevelopmentBranch == "" || resolution.DevelopmentVersion == "" {
			return Reservation{}, newReleaseError(ErrorClassStateConflict, "invalid_reservation_evidence")
		}
		reservation.GenerationVersion = resolution.DevelopmentVersion
		reservation.BranchIntents = []BranchIntent{{Ref: "refs/heads/" + resolution.ReleaseBranch, DesiredSHA: "pending"}, {Ref: "refs/heads/" + resolution.DevelopmentBranch, DesiredSHA: "pending"}}
	case "release:promote", "release:release":
		if source.Ref != "refs/heads/"+resolution.ReleaseBranch || resolution.DevelopmentVersion != "" {
			return Reservation{}, newReleaseError(ErrorClassStateConflict, "invalid_reservation_evidence")
		}
		reservation.GenerationVersion = resolution.ResolvedVersion
		reservation.BranchIntents = []BranchIntent{{Ref: source.Ref, ExpectedOldSHA: source.SHA, DesiredSHA: "pending"}}
	default:
		return Reservation{}, newReleaseError(ErrorClassStateConflict, "invalid_reservation_evidence")
	}
	if err := validateNewReservation(reservation); err != nil {
		return Reservation{}, err
	}
	return reservation, nil
}
