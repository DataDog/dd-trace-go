// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import "testing"

func TestStateV3BoundedOutcomePredicateRejectsForgedExpectationSpan(t *testing.T) {
	expected := StateV3BoundedClaimAcquireOutcomeExpectation{
		armBlobOID:       stateV3BoundedExpectationSpan{start: stateV3BoundedCompactDocumentBytes, length: 1},
		responseAttempts: 1,
	}
	outcome := StateV3BoundedCoordinationOutcome{document: stateV3BoundedDocument{kind: stateV3BoundedOutcome}}
	if StateV3BoundedOutcomeMatchesClaimAcquire(outcome, expected) {
		t.Fatal("forged expectation span matched")
	}
}

func TestStateV3BoundedClaimPredicateRejectsForgedExpectationSpan(t *testing.T) {
	expected := StateV3BoundedActiveClaimExpectation{
		command: stateV3BoundedExpectationSpan{start: stateV3BoundedCompactDocumentBytes, length: 1},
	}
	claim := StateV3BoundedCoordinationClaim{document: stateV3BoundedDocument{kind: stateV3BoundedClaim}}
	if StateV3BoundedClaimMatchesActiveReservation(claim, expected) {
		t.Fatal("forged expectation span matched")
	}
}
