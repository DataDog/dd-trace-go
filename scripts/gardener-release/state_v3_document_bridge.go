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
