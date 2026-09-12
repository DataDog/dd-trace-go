// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "bytes"

// ValidateStateV3Policy validates a supplied v3 policy without providing a
// default or selecting a runtime policy. It is a narrow read-only bridge for
// the internal snapshot collector's bounded preflight.
func ValidateStateV3Policy(policy StateV3Policy) error {
	return validateStateV3Policy(policy)
}

// ValidateStateV3Authentication validates a fully assembled lane snapshot
// against the authoritative lane-history and lifecycle rules. It binds raw to
// both the supplied record and the current authenticated snapshot.
//
// This is a read-only, non-authorizing bridge for the internal snapshot
// assembler. Callers must still use ValidateStateV3Record as the final gate:
// it additionally validates coordination and cross-lane claim authority.
func ValidateStateV3Authentication(raw []byte, record StateV3Record, policy StateV3Policy, authentication StateV3Authentication) error {
	invalid := func() error { return newReleaseError(ErrorClassStateConflict, "invalid_state_v3_authentication") }
	canonical, err := canonicalJSON(record)
	if err != nil || validateStateV3Policy(policy) != nil || !bytes.Equal(raw, canonical) || !bytes.Equal(raw, authentication.Current.RawRecord) || !validStateV3Authentication(authentication, policy, record) {
		return invalid()
	}
	return nil
}

// ValidateStateV3CoordinationAuthentication validates a fully assembled,
// checkpoint-bounded coordination snapshot using the authoritative claim and
// lane-termination rules. It does not authorize a lane transition.
func ValidateStateV3CoordinationAuthentication(authentication StateV3CoordinationAuthentication, policy StateV3Policy) error {
	if validateStateV3Policy(policy) != nil || !validStateV3CoordinationAuthentication(authentication, policy) {
		return newReleaseError(ErrorClassStateConflict, "invalid_state_v3_coordination_authentication")
	}
	return nil
}

// DeriveStateV3TreeChanges derives canonical parent-to-child tree changes
// only from complete, narrow state-tree evidence. It rejects malformed or
// incomplete evidence instead of normalizing it.
func DeriveStateV3TreeChanges(parent, child StateV3StateTreeEvidence) ([]StateV3ChangedPath, error) {
	if !validStateV3CompleteTree(parent) || !validStateV3CompleteTree(child) {
		return nil, newReleaseError(ErrorClassStateConflict, "invalid_state_v3_tree_evidence")
	}
	return stateV3TreeChanges(parent, child), nil
}
