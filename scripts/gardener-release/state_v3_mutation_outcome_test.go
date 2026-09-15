// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"fmt"
	"testing"
)

func TestStateV3CoordinationMutationOutcomeDocument(t *testing.T) {
	for _, operation := range []string{"claim_acquire", "claim_release"} {
		t.Run(operation, func(t *testing.T) {
			value := fixtureStateV3CoordinationMutationOutcome(t, operation)
			raw, err := canonicalJSON(value)
			if err != nil {
				t.Fatal(err)
			}
			if !ValidateStateV3CoordinationMutationOutcomeDocument(raw) || !ValidateStateV3CoordinationMutationOutcomeDocumentPath(raw, StateV3CoordinationMutationOutcomePath) || ValidateStateV3CoordinationMutationOutcomeDocumentPath(raw, "mutation-outcomes/2.11.json") {
				t.Fatalf("valid outcome rejected: %s", raw)
			}
			decoded, ok := DecodeStateV3CoordinationMutationOutcomeDocument(raw)
			if !ok || decoded != value {
				t.Fatalf("decoded=%#v ok=%v", decoded, ok)
			}
		})
	}
}

func TestStateV3CoordinationMutationOutcomeDocumentAcceptsLostOutcomeWithoutResponseOID(t *testing.T) {
	value := fixtureStateV3CoordinationMutationOutcome(t, "claim_release")
	value.ObservedRefOID = value.ExpectedHeadOID
	value.Response = StateV3MutationResponse{Observation: "lost", Attempts: 1}
	raw, err := canonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidateStateV3CoordinationMutationOutcomeDocument(raw) {
		t.Fatalf("lost outcome rejected: %s", raw)
	}
}

func TestStateV3CoordinationMutationOutcomeDocumentAcceptsLostAcquireWithoutEffectClaim(t *testing.T) {
	value := fixtureStateV3CoordinationMutationOutcome(t, "claim_acquire")
	value.ObservedRefOID = value.ExpectedHeadOID
	value.Response = StateV3MutationResponse{Observation: "lost", Attempts: 1}
	value.ClaimBlobOID = ""
	value.ClaimSHA256 = ""
	value.ClaimCommitOID = ""
	value.ClaimTreeOID = ""
	raw, err := canonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidateStateV3CoordinationMutationOutcomeDocument(raw) {
		t.Fatalf("lost acquisition rejected: %s", raw)
	}
}

func TestStateV3CoordinationMutationOutcomeDocumentRejectsInvalidFacts(t *testing.T) {
	valid := fixtureStateV3CoordinationMutationOutcome(t, "claim_acquire")
	for name, mutate := range map[string]func(*StateV3CoordinationMutationOutcome){
		"schema":           func(value *StateV3CoordinationMutationOutcome) { value.SchemaVersion = "2" },
		"operation":        func(value *StateV3CoordinationMutationOutcome) { value.Operation = "tag" },
		"state ref":        func(value *StateV3CoordinationMutationOutcome) { value.StateRef = StateV3MinorStateRef },
		"arm path":         func(value *StateV3CoordinationMutationOutcome) { value.ArmPath = "arm.json" },
		"claim path":       func(value *StateV3CoordinationMutationOutcome) { value.ClaimPath = "release-lines/2.12.json" },
		"release line":     func(value *StateV3CoordinationMutationOutcome) { value.ReleaseLine = "v02.11" },
		"resolved version": func(value *StateV3CoordinationMutationOutcome) { value.ResolvedVersion = "v2.12.1" },
		"lane":             func(value *StateV3CoordinationMutationOutcome) { value.LaneRef = "refs/heads/main" },
		"patch version minor lane": func(value *StateV3CoordinationMutationOutcome) {
			value.LaneRef = StateV3MinorStateRef
		},
		"minor version patch lane": func(value *StateV3CoordinationMutationOutcome) {
			value.ResolvedVersion, value.LaneRef = "v2.11.0", StateV3PatchStateRef
		},
		"bad oid":                  func(value *StateV3CoordinationMutationOutcome) { value.ArmBlobOID = "bad" },
		"bad digest":               func(value *StateV3CoordinationMutationOutcome) { value.ArmSHA256 = "bad" },
		"missing request digest":   func(value *StateV3CoordinationMutationOutcome) { value.RequestSHA256 = "" },
		"bad claim blob":           func(value *StateV3CoordinationMutationOutcome) { value.ClaimBlobOID = "bad" },
		"bad claim digest":         func(value *StateV3CoordinationMutationOutcome) { value.ClaimSHA256 = "bad" },
		"bad claim commit":         func(value *StateV3CoordinationMutationOutcome) { value.ClaimCommitOID = "bad" },
		"bad claim tree":           func(value *StateV3CoordinationMutationOutcome) { value.ClaimTreeOID = "bad" },
		"arm is not expected head": func(value *StateV3CoordinationMutationOutcome) { value.ArmCommitOID = oidForOutcome(95) },
		"observed unchanged head": func(value *StateV3CoordinationMutationOutcome) {
			value.Response.OID, value.ObservedRefOID = value.ExpectedHeadOID, value.ExpectedHeadOID
		},
		"lost oid": func(value *StateV3CoordinationMutationOutcome) {
			value.Response = StateV3MutationResponse{Observation: "lost", Attempts: 1, OID: value.ObservedRefOID}
		},
		"observed mismatch": func(value *StateV3CoordinationMutationOutcome) { value.Response.OID = oidForOutcome(90) },
		"not attempted": func(value *StateV3CoordinationMutationOutcome) {
			value.Response = StateV3MutationResponse{Observation: "not_attempted", Attempts: 0}
		},
		"acquire old claim":                 func(value *StateV3CoordinationMutationOutcome) { value.ExpectedClaimBlobOID = oidForOutcome(91) },
		"acquire claim digest mismatch":     func(value *StateV3CoordinationMutationOutcome) { value.ClaimSHA256 = digestForOutcome(91) },
		"acquire claim not effect snapshot": func(value *StateV3CoordinationMutationOutcome) { value.ClaimCommitOID = oidForOutcome(91) },
		"lost acquire effect claim": func(value *StateV3CoordinationMutationOutcome) {
			value.Response = StateV3MutationResponse{Observation: "lost", Attempts: 1}
		},
		"lost acquire partial effect claim": func(value *StateV3CoordinationMutationOutcome) {
			value.Response = StateV3MutationResponse{Observation: "lost", Attempts: 1}
			value.ClaimBlobOID = ""
			value.ClaimSHA256 = ""
			value.ClaimCommitOID = ""
		},
		"release missing claim": func(value *StateV3CoordinationMutationOutcome) {
			value.Operation = "claim_release"
			value.ExpectedClaimBlobOID, value.IntendedClaimSHA256 = "", ""
		},
		"release claim blob mismatch": func(value *StateV3CoordinationMutationOutcome) {
			value.Operation, value.ExpectedClaimBlobOID, value.IntendedClaimSHA256 = "claim_release", oidForOutcome(91), ""
		},
		"release claim not arm snapshot": func(value *StateV3CoordinationMutationOutcome) {
			value.Operation, value.ExpectedClaimBlobOID, value.IntendedClaimSHA256 = "claim_release", value.ClaimBlobOID, ""
			value.ClaimCommitOID = oidForOutcome(91)
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			raw, err := canonicalJSON(value)
			if err != nil {
				t.Fatal(err)
			}
			if ValidateStateV3CoordinationMutationOutcomeDocument(raw) {
				t.Fatalf("invalid outcome accepted: %s", raw)
			}
		})
	}
}

func TestStateV3CoordinationMutationOutcomeDocumentRejectsNonCanonicalAndOverLimit(t *testing.T) {
	value := fixtureStateV3CoordinationMutationOutcome(t, "claim_acquire")
	raw, err := canonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if ValidateStateV3CoordinationMutationOutcomeDocument(append([]byte(" "), raw...)) {
		t.Fatal("noncanonical outcome accepted")
	}
	duplicate := bytes.Replace(raw, []byte(`"operation":"claim_acquire"`), []byte(`"operation":"claim_acquire","operation":"claim_acquire"`), 1)
	if ValidateStateV3CoordinationMutationOutcomeDocument(duplicate) {
		t.Fatal("duplicate outcome key accepted")
	}
	assertStateV3DocumentOverLimit(t, "coordination outcome", raw, MaxStateV3CoordinationOutcomeBytes, &StateV3CoordinationMutationOutcome{})
}

func fixtureStateV3CoordinationMutationOutcome(t *testing.T, operation string) StateV3CoordinationMutationOutcome {
	t.Helper()
	value := StateV3CoordinationMutationOutcome{
		SchemaVersion: "1", Operation: operation, StateRef: StateV3CoordinationRef,
		ArmPath: stateV3CoordinationArmPath, ArmBlobOID: oidForOutcome(1), ArmSHA256: digestForOutcome(2),
		ArmCommitOID: oidForOutcome(5), ArmTreeOID: oidForOutcome(4), ClaimPath: "release-lines/2.11.json",
		RequestKey: "request-1", RequestSHA256: digestForOutcome(9), ReleaseLine: "v2.11", LaneRef: StateV3PatchStateRef,
		ResolvedVersion: "v2.11.1", ExpectedHeadOID: oidForOutcome(5), ClaimBlobOID: oidForOutcome(8),
		ClaimSHA256: digestForOutcome(7), ClaimCommitOID: oidForOutcome(6), ClaimTreeOID: oidForOutcome(10),
		Response: StateV3MutationResponse{Observation: "observed", Attempts: 1, OID: oidForOutcome(6)}, ObservedRefOID: oidForOutcome(6),
	}
	if operation == "claim_acquire" {
		value.IntendedClaimSHA256 = digestForOutcome(7)
	} else {
		value.ExpectedClaimBlobOID = value.ClaimBlobOID
		value.ClaimCommitOID = value.ExpectedHeadOID
		value.ClaimTreeOID = value.ArmTreeOID
	}
	return value
}

func oidForOutcome(value int) string    { return fmt.Sprintf("%040x", value) }
func digestForOutcome(value int) string { return fmt.Sprintf("%064x", value) }

func TestStateV3CoordinationMutationOutcomeDocumentRejectsReleaseClaimIdentityMismatches(t *testing.T) {
	for name, mutate := range map[string]func(*StateV3CoordinationMutationOutcome){
		"request digest": func(value *StateV3CoordinationMutationOutcome) {
			value.RequestSHA256 = ""
		},
		"claim blob": func(value *StateV3CoordinationMutationOutcome) {
			value.ClaimBlobOID = oidForOutcome(92)
		},
		"claim digest": func(value *StateV3CoordinationMutationOutcome) {
			value.ClaimSHA256 = ""
		},
		"claim commit": func(value *StateV3CoordinationMutationOutcome) {
			value.ClaimCommitOID = oidForOutcome(92)
		},
		"claim tree": func(value *StateV3CoordinationMutationOutcome) {
			value.ClaimTreeOID = ""
		},
		"claim tree not arm tree": func(value *StateV3CoordinationMutationOutcome) {
			value.ClaimTreeOID = oidForOutcome(92)
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := fixtureStateV3CoordinationMutationOutcome(t, "claim_release")
			mutate(&value)
			raw, err := canonicalJSON(value)
			if err != nil {
				t.Fatal(err)
			}
			if ValidateStateV3CoordinationMutationOutcomeDocument(raw) {
				t.Fatalf("invalid release outcome accepted: %s", raw)
			}
		})
	}
}
