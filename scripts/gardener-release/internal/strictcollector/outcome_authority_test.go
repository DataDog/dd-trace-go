// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog, Inc.
// Copyright 2026 Datadog, Inc.

package strictcollector

import (
	"go/ast"
	"testing"

	gardenerrelease "github.com/DataDog/dd-trace-go/scripts/gardener-release"
)

// TestCoordinationOutcomeAuthoritySurface keeps the new transcript role inside
// the existing generation/ordinal/token admission algebra. It deliberately
// forbids a second outcome-specific reader, decoder, handle, or accessor.
func TestCoordinationOutcomeAuthoritySurface(t *testing.T) {
	files, err := productionStrictcollectorFiles(".")
	if err != nil {
		t.Fatal(err)
	}
	for filename, file := range files {
		var invalid string
		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			switch ident.Name {
			case "stateV3DocumentCoordinationOutcome":
				if filename != "documents.go" {
					invalid = "outcome document kind outside fixed grammar"
				}
			case "StateV3CoordinationMutationOutcome", "DecodeStateV3CoordinationMutationOutcomeDocument", "ClaimBlobOID", "ClaimSHA256", "ClaimCommitOID", "ClaimTreeOID":
				invalid = "outcome semantic value, causal identity, or decoder escapes typed document validation"
			case "ValidateStateV3CoordinationMutationOutcomeDocumentPath":
				if filename != "documents.go" {
					invalid = "outcome path validator outside fixed grammar"
				}
			}
			return invalid == ""
		})
		if invalid != "" {
			t.Fatalf("%s: %s", filename, invalid)
		}
	}
	if !stateV3DocumentPathAllowed(stateV3AssemblyCoordination, stateV3DocumentCoordinationOutcome, gardenerrelease.StateV3CoordinationMutationOutcomePath) || stateV3DocumentPathAllowed(stateV3AssemblyMinor, stateV3DocumentCoordinationOutcome, gardenerrelease.StateV3CoordinationMutationOutcomePath) {
		t.Fatal("outcome path escaped coordination-only fixed grammar")
	}
	if stateV3DocumentValid(stateV3DocumentCoordinationOutcome, "mutation-outcomes/2.11.json", coordinationOutcomeRaw()) {
		t.Fatal("outcome accepted through caller-selected path")
	}
}
