// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "bytes"

// ValidateStateV3RecordDocument validates canonical state-record bytes.
func ValidateStateV3RecordDocument(raw []byte) bool {
	value, err := DecodeStateV3Record(raw)
	return err == nil && bytes.Equal(mustCanonicalStateV3(value), raw)
}

// ValidateStateV3ActiveLeaseDocument validates canonical lease bytes.
func ValidateStateV3ActiveLeaseDocument(raw []byte) bool {
	var value StateV3ActiveOperationLease
	return decodeStateV3Document(raw, MaxStateV3ActiveLeaseBytes, &value) == nil && value.SchemaVersion == "1" && bytes.Equal(mustCanonicalStateV3(value), raw)
}

// ValidateStateV3ReservationDocument validates canonical reservation bytes.
func ValidateStateV3ReservationDocument(raw []byte) bool {
	var value StateV3Reservation
	return decodeStateV3Document(raw, MaxStateV3ReservationBytes, &value) == nil && bytes.Equal(mustCanonicalStateV3(value), raw)
}

// ValidateStateV3PreparedManifestDocument validates canonical prepared-manifest bytes.
func ValidateStateV3PreparedManifestDocument(raw []byte) bool {
	_, _, ok := DecodeStateV3PreparedManifestDocument(raw)
	return ok
}

// DecodeStateV3PreparedManifestDocument returns only the semantic facts encoded
// in a canonical prepared manifest. It performs no I/O or authorization.
func DecodeStateV3PreparedManifestDocument(raw []byte) (StateV3PreparedState, []StateV3TagPlan, bool) {
	var value stateV3PreparedManifest
	if decodeStateV3Document(raw, MaxStateV3PreparedManifestBytes, &value) != nil || !bytes.Equal(mustCanonicalStateV3(value), raw) {
		return StateV3PreparedState{}, nil, false
	}
	return StateV3PreparedState{
		Bundle: value.Bundle, AdditionCount: value.AdditionCount,
		TotalDecodedAdditionBytes: value.TotalDecodedAdditionBytes,
		ToolDigest:                value.ToolDigest, ValidatorDigest: value.ValidatorDigest,
		Mutation: value.Mutation,
	}, value.TagPlans, true
}

// ValidateStateV3CoordinationArmDocument validates canonical coordination-arm bytes.
func ValidateStateV3CoordinationArmDocument(raw []byte) bool {
	var value StateV3CoordinationMutationArm
	return decodeStateV3Document(raw, MaxStateV3CoordinationArmBytes, &value) == nil && bytes.Equal(mustCanonicalStateV3(value), raw)
}

// DecodeStateV3CoordinationMutationOutcomeDocument returns canonical immutable
// transcript facts only. It performs no I/O, outcome observation, or final
// authorization; a later assembly must authenticate the referenced arm and
// independently reread post-mutation state before using this document.
func DecodeStateV3CoordinationMutationOutcomeDocument(raw []byte) (StateV3CoordinationMutationOutcome, bool) {
	var value StateV3CoordinationMutationOutcome
	if decodeStateV3Document(raw, MaxStateV3CoordinationOutcomeBytes, &value) != nil || !validStateV3CoordinationMutationOutcome(value) || !bytes.Equal(mustCanonicalStateV3(value), raw) {
		return StateV3CoordinationMutationOutcome{}, false
	}
	return value, true
}

// ValidateStateV3CoordinationMutationOutcomeDocument validates canonical
// transcript bytes. It does not turn a transcript into a mutation response.
func ValidateStateV3CoordinationMutationOutcomeDocument(raw []byte) bool {
	_, ok := DecodeStateV3CoordinationMutationOutcomeDocument(raw)
	return ok
}

// ValidateStateV3CoordinationMutationOutcomeDocumentPath additionally binds a
// transcript to the sole approved coordination-tree leaf.
func ValidateStateV3CoordinationMutationOutcomeDocumentPath(raw []byte, path string) bool {
	return path == StateV3CoordinationMutationOutcomePath && ValidateStateV3CoordinationMutationOutcomeDocument(raw)
}

func validStateV3CoordinationMutationOutcome(value StateV3CoordinationMutationOutcome) bool {
	line, lineErr := parseReleaseLine(value.ReleaseLine)
	version, versionErr := ParseReleaseVersion(value.ResolvedVersion)
	if value.SchemaVersion != "1" || (value.Operation != "claim_acquire" && value.Operation != "claim_release") || value.StateRef != StateV3CoordinationRef || value.ArmPath != stateV3CoordinationArmPath || !validStateV3OID(value.ArmBlobOID) || !lowerHexDigest(value.ArmSHA256) || !validStateV3OID(value.ArmCommitOID) || !validStateV3OID(value.ArmTreeOID) || lineErr != nil || versionErr != nil || version.Major != line.Major || version.Minor != line.Minor || !validStateV3Text(value.RequestKey, 256) || !lowerHexDigest(value.RequestSHA256) || !validStateV3OID(value.ExpectedHeadOID) || !validStateV3OID(value.ObservedRefOID) || value.ArmCommitOID != value.ExpectedHeadOID || !validStateV3CoordinationAttemptResponse(value.Response, value.ObservedRefOID) {
		return false
	}
	expectedLane := StateV3PatchStateRef
	if version.Patch == 0 {
		expectedLane = StateV3MinorStateRef
	}
	if value.LaneRef != expectedLane {
		return false
	}
	claimPath, pathOK := stateV3CoordinationClaimPath(value.ReleaseLine)
	if !pathOK || value.ClaimPath != claimPath {
		return false
	}
	if value.Response.Observation != "observed" || value.ObservedRefOID == value.ExpectedHeadOID {
		return false
	}
	switch value.Operation {
	case "claim_acquire":
		if value.ExpectedClaimBlobOID != "" || !lowerHexDigest(value.IntendedClaimSHA256) {
			return false
		}
		return validStateV3OID(value.ClaimBlobOID) && value.ClaimSHA256 == value.IntendedClaimSHA256 && validStateV3OID(value.ClaimCommitOID) && value.ClaimCommitOID == value.ObservedRefOID && validStateV3OID(value.ClaimTreeOID)
	case "claim_release":
		return validStateV3OID(value.ExpectedClaimBlobOID) && value.ClaimBlobOID == value.ExpectedClaimBlobOID && lowerHexDigest(value.ClaimSHA256) && value.IntendedClaimSHA256 == "" && validStateV3OID(value.ClaimCommitOID) && value.ClaimCommitOID == value.ExpectedHeadOID && validStateV3OID(value.ClaimTreeOID) && value.ClaimTreeOID == value.ArmTreeOID
	default:
		return false
	}
}

// ValidateStateV3ReleaseLineClaimDocument validates canonical claim bytes.
func ValidateStateV3ReleaseLineClaimDocument(raw []byte) bool {
	var value StateV3ReleaseLineClaim
	return decodeStateV3Document(raw, MaxStateV3CoordinationClaimBytes, &value) == nil && bytes.Equal(mustCanonicalStateV3(value), raw)
}

// ValidateStateV3StateDocumentPath delegates to the authoritative state-tree
// path grammar without exposing its parser.
func ValidateStateV3StateDocumentPath(path string) bool {
	return validStateV3StatePath(path)
}

// ValidateStateV3RecordDocumentPath validates canonical record bytes and
// binds the document to the request directory derived from its reservation.
func ValidateStateV3RecordDocumentPath(raw []byte, path string) bool {
	value, err := DecodeStateV3Record(raw)
	if err != nil || !bytes.Equal(mustCanonicalStateV3(value), raw) || !validID(value.Reservation.RepositoryID) || !validID(value.Reservation.OriginalCommentID) {
		return false
	}
	return path == "requests/"+value.Reservation.RepositoryID+"/"+value.Reservation.OriginalCommentID+"/state.json"
}

// ValidateStateV3ReservationDocumentPath validates canonical reservation
// bytes and binds them to its own authoritative request directory.
func ValidateStateV3ReservationDocumentPath(raw []byte, path string) bool {
	var value StateV3Reservation
	if decodeStateV3Document(raw, MaxStateV3ReservationBytes, &value) != nil || !bytes.Equal(mustCanonicalStateV3(value), raw) {
		return false
	}
	paths, ok := stateV3PreparedPaths(value.RepositoryID, value.OriginalCommentID)
	return ok && path == paths.Reservation
}

// ValidateStateV3CoordinationClaimPath delegates to the authoritative claim
// path grammar without exposing its release-line parser.
func ValidateStateV3CoordinationClaimPath(path string) bool {
	return validStateV3CoordinationPath(path)
}

// ValidateStateV3ReleaseLineClaimDocumentPath validates canonical claim bytes
// and binds their release line to the authoritative claim path.
func ValidateStateV3ReleaseLineClaimDocumentPath(raw []byte, path string) bool {
	var value StateV3ReleaseLineClaim
	if decodeStateV3Document(raw, MaxStateV3CoordinationClaimBytes, &value) != nil || !bytes.Equal(mustCanonicalStateV3(value), raw) {
		return false
	}
	expected, ok := stateV3CoordinationClaimPath(value.ReleaseLine)
	return ok && path == expected
}

func mustCanonicalStateV3(value any) []byte {
	raw, err := canonicalJSON(value)
	if err != nil {
		return nil
	}
	return raw
}
