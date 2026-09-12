// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

func validStateV3StagedEnvelope(envelope StateV3StagedEnvelopeEvidence, reservation StateV3Reservation, policy StateV3Policy) bool {
	if !validStateV3Prepared(envelope.Prepared, reservation, policy) || !validStateV3TagPlans(envelope.TagPlans, envelope.Prepared.Mutation, reservation, policy) || len(envelope.Files) != StateV3PreparedAdditionCount {
		return false
	}
	manifest := stateV3PreparedManifest{SchemaVersion: StateV3SchemaVersion, Bundle: envelope.Prepared.Bundle, AdditionCount: envelope.Prepared.AdditionCount, TotalDecodedAdditionBytes: envelope.Prepared.TotalDecodedAdditionBytes, ToolDigest: envelope.Prepared.ToolDigest, ValidatorDigest: envelope.Prepared.ValidatorDigest, Mutation: envelope.Prepared.Mutation, TagPlans: envelope.TagPlans}
	preparedRaw, err := canonicalJSON(manifest)
	if err != nil {
		return false
	}
	reservationRaw, err := canonicalJSON(reservation)
	if err != nil {
		return false
	}
	expectedRaw := [][]byte{envelope.Files[0].Raw, preparedRaw, reservationRaw}
	for index, file := range envelope.Files {
		expected := envelope.Prepared.StateFiles[index]
		if file.Path != expected.Path || file.Path != stateV3PreparedFilePaths(envelope.Prepared)[index] || !bytes.Equal(file.Raw, expectedRaw[index]) || file.SizeBytes != int64(len(file.Raw)) || file.SizeBytes != expected.SizeBytes || file.SHA256 != expected.SHA256 || file.BlobOID != expected.BlobOID {
			return false
		}
		digest := sha256.Sum256(file.Raw)
		if file.SHA256 != hex.EncodeToString(digest[:]) || file.BlobOID != stateV3GitBlobOID(file.Raw) {
			return false
		}
	}
	return envelope.Files[0].BlobOID == envelope.Prepared.Bundle.BlobOID && envelope.Files[0].SHA256 == envelope.Prepared.Bundle.SHA256 && envelope.Files[0].SizeBytes == envelope.Prepared.Bundle.SizeBytes
}

func stateV3PreparedFilePaths(prepared StateV3PreparedState) []string {
	return []string{prepared.Bundle.Path, strings.TrimSuffix(prepared.Bundle.Path, stateV3GenerationBundleFile) + stateV3PreparedFile, strings.TrimSuffix(prepared.Bundle.Path, stateV3GenerationBundleFile) + stateV3ReservationFile}
}

func validStateV3Prepared(prepared StateV3PreparedState, reservation StateV3Reservation, policy StateV3Policy) bool {
	paths, ok := stateV3PreparedPaths(reservation.RepositoryID, reservation.OriginalCommentID)
	if !ok || prepared.AdditionCount != StateV3PreparedAdditionCount || len(prepared.StateFiles) != StateV3PreparedAdditionCount || prepared.Bundle.Path != paths.Bundle || prepared.Bundle.SizeBytes <= 0 || prepared.Bundle.SizeBytes > policy.Limits.PreparedBundleBytes || prepared.Bundle.SizeBytes > MaxStateV3PreparedBundleBytes || !lowerHexDigest(prepared.Bundle.SHA256) || !validStateV3OID(prepared.Bundle.BlobOID) || prepared.Bundle.PrerequisiteOID != reservation.SourceOID || !lowerHexDigest(prepared.ToolDigest) || !lowerHexDigest(prepared.ValidatorDigest) {
		return false
	}
	if prepared.TotalDecodedAdditionBytes <= 0 || prepared.TotalDecodedAdditionBytes > policy.Limits.DecodedAdditionBytes || prepared.TotalDecodedAdditionBytes > MaxStateV3DecodedAdditionBytes {
		return false
	}
	expectedPaths := []string{paths.Bundle, paths.Prepared, paths.Reservation}
	var total int64
	for index, file := range prepared.StateFiles {
		if file.Path != expectedPaths[index] || !validStateV3OID(file.BlobOID) || !lowerHexDigest(file.SHA256) || file.SizeBytes <= 0 {
			return false
		}
		total += file.SizeBytes
	}
	if total != prepared.TotalDecodedAdditionBytes || prepared.StateFiles[0].Path != prepared.Bundle.Path || prepared.StateFiles[0].BlobOID != prepared.Bundle.BlobOID || prepared.StateFiles[0].SHA256 != prepared.Bundle.SHA256 || prepared.StateFiles[0].SizeBytes != prepared.Bundle.SizeBytes {
		return false
	}
	return validStateV3Mutation(prepared.Mutation, reservation, policy)
}

func validStateV3Mutation(intent StateV3CommitMutationIntent, reservation StateV3Reservation, policy StateV3Policy) bool {
	target, ok := stateV3TargetRef(reservation.Command, reservation.ReleaseLine)
	if !ok || intent.RepositoryID != policy.RepositoryID || intent.RepositoryFullName != policy.RepositoryFullName || intent.TargetRef != target || intent.ExpectedHeadOID != reservation.SourceOID || !validStateV3OID(intent.ExpectedTreeOID) || intent.ExpectedTreeOID == reservation.SourceOID || intent.Message != "release: "+reservation.GenerationVersion || len(intent.FileChanges) == 0 || len(intent.FileChanges) > policy.Limits.ReleaseFileChanges || len(intent.FileChanges) > MaxStateV3ReleaseFileChanges || !lowerHexDigest(intent.FileChangesSHA256) || !matchesStateV3BranchIntents(intent.Branches, reservation) {
		return false
	}
	if !sort.SliceIsSorted(intent.FileChanges, func(i, j int) bool { return intent.FileChanges[i].Path < intent.FileChanges[j].Path }) {
		return false
	}
	seen := map[string]bool{}
	for _, change := range intent.FileChanges {
		if !validRepositoryRelativePath(change.Path) || seen[change.Path] {
			return false
		}
		seen[change.Path] = true
		switch change.Operation {
		case "addition":
			if change.Mode != "100644" || !validStateV3OID(change.BlobOID) || !lowerHexDigest(change.SHA256) || change.SizeBytes <= 0 {
				return false
			}
		case "deletion":
			if change.Mode != "" || change.BlobOID != "" || change.SHA256 != "" || change.SizeBytes != 0 {
				return false
			}
		default:
			return false
		}
	}
	digest, err := stateV3FileChangesDigest(intent.FileChanges)
	return err == nil && digest == intent.FileChangesSHA256
}

func matchesStateV3BranchIntents(branches []StateV3BranchMutationIntent, reservation StateV3Reservation) bool {
	line, err := parseReleaseLine(reservation.ReleaseLine)
	if err != nil {
		return false
	}
	expected := []StateV3BranchMutationIntent{{
		Ref:            "refs/heads/" + releaseBranchName(line.Major, line.Minor),
		ExpectedOldOID: reservation.SourceOID,
		Target:         "platform_commit",
	}}
	if reservation.Command == "release:prepare" {
		releaseRef := "refs/heads/" + releaseBranchName(line.Major, line.Minor)
		developmentRef := "refs/heads/" + devBranchName(line.Major, line.Minor+1)
		expected = []StateV3BranchMutationIntent{
			{Ref: releaseRef, Target: "source"},
			{Ref: developmentRef, Target: "source"},
			{Ref: developmentRef, ExpectedOldOID: reservation.SourceOID, Target: "platform_commit"},
		}
	}
	if len(branches) != len(expected) {
		return false
	}
	for index := range branches {
		if branches[index] != expected[index] {
			return false
		}
	}
	return true
}

func validStateV3Commit(commit StateV3AdoptedCommit, prepared StateV3PreparedState, policy StateV3Policy) bool {
	intent := prepared.Mutation
	if !validStateV3OID(commit.OID) || commit.OID == intent.ExpectedHeadOID || !validStateV3MutationResponse(commit.MutationResponse, commit.OID) || commit.OID != commit.ObservedRefOID || commit.OID != commit.RESTOID || commit.OID != commit.GraphQLOID || commit.Ref != intent.TargetRef || commit.ParentOID != intent.ExpectedHeadOID || commit.TreeOID != intent.ExpectedTreeOID || commit.Message != intent.Message || commit.FileChangesSHA256 != intent.FileChangesSHA256 || len(commit.FileChanges) != len(intent.FileChanges) {
		return false
	}
	for index := range commit.FileChanges {
		if commit.FileChanges[index] != intent.FileChanges[index] {
			return false
		}
	}
	return commit.RESTVerified && commit.RESTReason == StateV3RequiredRESTVerificationReason && commit.GraphQLSignatureValid && commit.WasSignedByGitHub && commit.SignatureState == StateV3RequiredSignatureState && commit.Roles == policy.CommitRoles
}
