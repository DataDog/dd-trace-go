// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package agenteval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The schema check runs against the repository itself rather than a fixture, so
// it validates the same Orchestrion version the tree pins and fails if that
// version's schema ever stops accepting the aspects already in the repo.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, orchestrionSchemaAnchor)); err != nil {
		t.Skipf("not a full checkout: %v", err)
	}
	return root
}

func TestValidateOrchestrionYAMLAcceptsRealAspects(t *testing.T) {
	if testing.Short() {
		t.Skip("resolves the orchestrion module")
	}
	root := repoRoot(t)
	for _, rel := range []string{
		"contrib/valkey-io/valkey-go/orchestrion.yml",
		"contrib/twmb/franz-go/orchestrion.yml",
	} {
		if err := ValidateOrchestrionYAML(context.Background(), root, rel); err != nil {
			t.Errorf("%s should conform to the orchestrion schema: %v", rel, err)
		}
	}
}

// Parsing as YAML is not the bar. These all parse and all fail the schema, and
// each is silent at build time: the aspect is ignored and instrumentation
// quietly does nothing.
// Parsing as YAML is not the bar. These all parse and all fail the schema, and
// each is silent at build time: the aspect is ignored and instrumentation
// quietly does nothing.
func TestValidateOrchestrionYAMLRejectsMalformed(t *testing.T) {
	if testing.Short() {
		t.Skip("resolves the orchestrion module")
	}
	root := repoRoot(t)

	// A throwaway tree needs only the anchor module for schema resolution, so
	// copy its go.mod and go.sum across rather than writing into the checkout.
	tree := t.TempDir()
	anchor := filepath.Join(tree, orchestrionSchemaAnchor)
	if err := os.MkdirAll(anchor, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		body, err := os.ReadFile(filepath.Join(root, orchestrionSchemaAnchor, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(anchor, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		body string
	}{
		{"missing meta", "aspects:\n  - id: x\n"},
		{"meta without name", "meta:\n  description: d\naspects: []\n"},
		{"neither aspects nor extends", "meta:\n  name: n\n  description: d\n"},
		{"aspects is not a list", "meta:\n  name: n\n  description: d\naspects: nope\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rel := strings.ReplaceAll(tc.name, " ", "-") + ".yml"
			if err := os.WriteFile(filepath.Join(tree, rel), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := ValidateOrchestrionYAML(context.Background(), tree, rel); err == nil {
				t.Errorf("%q parses as YAML but must fail the schema", tc.body)
			}
		})
	}
}
