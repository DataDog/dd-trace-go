// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"bytes"
	"reflect"
	"testing"
)

func TestValidateStateV3PolicyBridgePreservesAuthoritativeValidation(t *testing.T) {
	if err := ValidateStateV3Policy(fixtureStateV3Policy()); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	maximum := fixtureStateV3Policy()
	maximum.StateLanes.Minor.MaxHistoryCommits = MaxStateV3HistoryCommits
	if err := ValidateStateV3Policy(maximum); err != nil {
		t.Fatalf("maximum valid policy rejected: %v", err)
	}
	invalid := fixtureStateV3Policy()
	invalid.Coordination.MaxHistoryCommits = 1
	if err := ValidateStateV3Policy(invalid); err == nil {
		t.Fatal("invalid policy accepted")
	}
}

func TestValidateStateV3AuthenticationBridgePreservesLaneAuthentication(t *testing.T) {
	record := fixtureStateV3Record(t)
	policy := fixtureStateV3Policy()
	authentication := fixtureStateV3Authentication(t, policy, record)
	raw, err := canonicalJSON(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateStateV3Authentication(raw, record, policy, authentication); err != nil {
		t.Fatalf("valid lane authentication rejected: %v", err)
	}

	for name, mutate := range map[string]func([]byte, *StateV3Authentication){
		"noncanonical raw": func(raw []byte, _ *StateV3Authentication) {
			raw[len(raw)-1] = ' '
		},
		"current raw mismatch": func(_ []byte, authentication *StateV3Authentication) {
			authentication.Current.RawRecord = append([]byte(nil), authentication.Current.RawRecord...)
			authentication.Current.RawRecord[0] = '{'
			authentication.Current.RawRecord[len(authentication.Current.RawRecord)-1] = ' '
		},
		"invalid history": func(_ []byte, authentication *StateV3Authentication) {
			authentication.Current.Tree.Truncated = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidateRaw := append([]byte(nil), raw...)
			candidate := cloneStateV3(t, authentication)
			mutate(candidateRaw, &candidate)
			if err := ValidateStateV3Authentication(candidateRaw, record, policy, candidate); err == nil {
				t.Fatal("invalid lane authentication accepted")
			}
		})
	}
}

func TestValidateStateV3AuthenticationBridgeIsNotFinalCrossLaneGate(t *testing.T) {
	record := fixtureStateV3Record(t)
	policy := fixtureStateV3Policy()
	authentication := fixtureStateV3Authentication(t, policy, record)
	raw, err := canonicalJSON(record)
	if err != nil {
		t.Fatal(err)
	}

	// The lane bridge intentionally validates only the complete lane snapshot.
	// ValidateStateV3Record is still required to enforce coordination and
	// same-release-line authority before any caller treats the state as usable.
	authentication.Coordination = nil
	if err := ValidateStateV3Authentication(raw, record, policy, authentication); err != nil {
		t.Fatalf("lane bridge unexpectedly enforced coordination: %v", err)
	}
	if err := ValidateStateV3Record(raw, record, policy, authentication); err == nil {
		t.Fatal("final record gate accepted missing coordination authority")
	}
}

func TestValidateStateV3CoordinationAuthenticationBridgePreservesAuthority(t *testing.T) {
	policy := fixtureStateV3Policy()
	authentication := fixtureStateV3CoordinationObservation(t).Authentication
	if err := ValidateStateV3CoordinationAuthentication(authentication, policy); err != nil {
		t.Fatalf("valid coordination authentication rejected: %v", err)
	}

	for name, mutate := range map[string]func(*StateV3CoordinationAuthentication){
		"wrong ref": func(authentication *StateV3CoordinationAuthentication) {
			authentication.StateRef = StateV3MinorStateRef
		},
		"invalid tree": func(authentication *StateV3CoordinationAuthentication) {
			authentication.Current.Tree.Complete = false
		},
		"invalid checkpoint": func(authentication *StateV3CoordinationAuthentication) {
			authentication.CheckpointOID = v3OIDa
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneStateV3(t, authentication)
			mutate(&candidate)
			if err := ValidateStateV3CoordinationAuthentication(candidate, policy); err == nil {
				t.Fatal("invalid coordination authentication accepted")
			}
		})
	}
}

func TestDeriveStateV3TreeChangesBridgeIsCanonicalAndStrict(t *testing.T) {
	parent := StateV3StateTreeEvidence{
		OID:      v3OIDa,
		Complete: true,
		Entries: []StateV3StateTreeEntry{
			{Path: StateV3ActiveLeasePath, Mode: "100644", Type: "blob", OID: v3OIDb},
			{Path: "requests/123/789/state.json", Mode: "100644", Type: "blob", OID: v3OIDc},
		},
	}
	child := StateV3StateTreeEvidence{
		OID:      v3OIDb,
		Complete: true,
		Entries: []StateV3StateTreeEntry{
			{Path: "requests/123/789/staged.json", Mode: "100644", Type: "blob", OID: v3OIDd},
			{Path: "requests/123/789/state.json", Mode: "100644", Type: "blob", OID: v3OIDa},
		},
	}
	changes, err := DeriveStateV3TreeChanges(parent, child)
	if err != nil {
		t.Fatalf("derive changes: %v", err)
	}
	want := []StateV3ChangedPath{
		{Path: StateV3ActiveLeasePath, ParentOID: v3OIDb},
		{Path: "requests/123/789/staged.json", ChildOID: v3OIDd},
		{Path: "requests/123/789/state.json", ParentOID: v3OIDc, ChildOID: v3OIDa},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes=%#v want=%#v", changes, want)
	}
	if !bytes.Equal([]byte(changes[0].Path), []byte(StateV3ActiveLeasePath)) {
		t.Fatal("canonical ordering changed")
	}

	for name, mutate := range map[string]func(*StateV3StateTreeEvidence){
		"incomplete": func(tree *StateV3StateTreeEvidence) { tree.Complete = false },
		"truncated":  func(tree *StateV3StateTreeEvidence) { tree.Truncated = true },
		"duplicate": func(tree *StateV3StateTreeEvidence) {
			tree.Entries = append(tree.Entries, tree.Entries[0])
		},
		"nonregular": func(tree *StateV3StateTreeEvidence) { tree.Entries[0].Mode = "100755" },
	} {
		t.Run(name, func(t *testing.T) {
			invalidParent := cloneStateV3(t, parent)
			mutate(&invalidParent)
			if _, err := DeriveStateV3TreeChanges(invalidParent, child); err == nil {
				t.Fatal("invalid parent tree accepted")
			}
		})
	}
}
