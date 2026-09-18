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

func TestApplyMutationDeletePaths(t *testing.T) {
	ctx := context.Background()
	tree := newRepo(t, map[string]string{
		"contrib/thing/orchestrion.yml": "meta: {}\n",
		"contrib/thing/thing.go":        "package thing\n",
	})

	m := Mutation{Kind: MutationDeletePaths, Paths: []string{"contrib/thing/orchestrion.yml"}}
	if err := ApplyMutation(ctx, tree, "", m); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree, "contrib/thing/orchestrion.yml")); !os.IsNotExist(err) {
		t.Errorf("target should be gone, err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree, "contrib/thing/thing.go")); err != nil {
		t.Errorf("unrelated file should survive: %v", err)
	}
}

func TestApplyMutationFailsOnStalePath(t *testing.T) {
	ctx := context.Background()
	tree := newRepo(t, map[string]string{"a.go": "package a\n"})

	m := Mutation{Kind: MutationDeletePaths, Paths: []string{"contrib/gone/orchestrion.yml"}}
	err := ApplyMutation(ctx, tree, "", m)
	if err == nil {
		t.Fatal("expected an error: silently running a stale record would produce a result that looks valid but measures nothing")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error should explain staleness, got: %v", err)
	}
}

func TestApplyMutationAllowsMissingPaths(t *testing.T) {
	ctx := context.Background()
	tree := newRepo(t, map[string]string{
		"contrib/thing/implementation.go": "package thing\n",
	})
	m := Mutation{
		Kind:         MutationDeletePaths,
		Paths:        []string{"contrib/thing/implementation.go", "contrib/thing/not-landed.go"},
		AllowMissing: true,
	}

	if err := CheckMutation(ctx, tree, "", m); err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := ApplyMutation(ctx, tree, "", m); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tree, "contrib/thing/implementation.go")); !os.IsNotExist(err) {
		t.Errorf("existing target should be gone, err = %v", err)
	}
}

func TestApplyMutationRejectsEscapingPath(t *testing.T) {
	ctx := context.Background()
	tree := newRepo(t, map[string]string{"a.go": "package a\n"})

	for _, rel := range []string{"../outside", "/etc/hosts"} {
		m := Mutation{Kind: MutationDeletePaths, Paths: []string{rel}}
		if err := ApplyMutation(ctx, tree, "", m); err == nil {
			t.Errorf("path %q should be rejected", rel)
		}
	}
}

func TestApplyMutationPatch(t *testing.T) {
	ctx := context.Background()
	tree := newRepo(t, map[string]string{
		"instrumentation/packages.go": "package instrumentation\n\nvar list = []string{\n\t\"valkey-io/valkey-go\",\n}\n",
	})
	mutationsDir := t.TempDir()
	// A patch that strips one registration line, which is the case delete_paths
	// cannot express.
	patch := `--- a/instrumentation/packages.go
+++ b/instrumentation/packages.go
@@ -1,5 +1,4 @@
 package instrumentation

 var list = []string{
-	"valkey-io/valkey-go",
 }
`
	if err := os.WriteFile(filepath.Join(mutationsDir, "strip.patch"), []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}

	m := Mutation{Kind: MutationApplyPatch, Patch: "strip.patch"}
	if err := CheckMutation(ctx, tree, mutationsDir, m); err != nil {
		t.Fatalf("check should pass before applying: %v", err)
	}
	if err := ApplyMutation(ctx, tree, mutationsDir, m); err != nil {
		t.Fatalf("apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(tree, "instrumentation/packages.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "valkey-io/valkey-go") {
		t.Errorf("registration line should be gone, got:\n%s", body)
	}
}

func TestCheckMutationIsNonDestructive(t *testing.T) {
	ctx := context.Background()
	tree := newRepo(t, map[string]string{"contrib/thing/orchestrion.yml": "meta: {}\n"})

	m := Mutation{Kind: MutationDeletePaths, Paths: []string{"contrib/thing/orchestrion.yml"}}
	if err := CheckMutation(ctx, tree, "", m); err != nil {
		t.Fatalf("check: %v", err)
	}
	// Being non-destructive is what lets a whole dataset be preflighted against one
	// materialised tree per ref instead of one per record.
	if _, err := os.Stat(filepath.Join(tree, "contrib/thing/orchestrion.yml")); err != nil {
		t.Errorf("CheckMutation must not modify the tree: %v", err)
	}
}

func TestCheckMutationRejectsPatchThatDoesNotApply(t *testing.T) {
	ctx := context.Background()
	tree := newRepo(t, map[string]string{"a.go": "package a\n"})
	mutationsDir := t.TempDir()
	patch := `--- a/a.go
+++ b/a.go
@@ -1 +1 @@
-package something_else
+package b
`
	if err := os.WriteFile(filepath.Join(mutationsDir, "bad.patch"), []byte(patch), 0o644); err != nil {
		t.Fatal(err)
	}

	err := CheckMutation(ctx, tree, mutationsDir, Mutation{Kind: MutationApplyPatch, Patch: "bad.patch"})
	if err == nil {
		t.Fatal("expected an error: a patch that applies to one ref but not the other means the two sides are not the same task")
	}
	if !strings.Contains(err.Error(), "does not apply") {
		t.Errorf("error = %v", err)
	}
}

func TestVerifyMutationsAcrossRefs(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t, map[string]string{
		"contrib/thing/orchestrion.yml": "meta: {}\n",
	})
	oldRef, err := ResolveRef(ctx, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the file moving on a later commit, which is exactly how a record
	// goes stale as main advances.
	if err := os.Remove(filepath.Join(repo, "contrib/thing/orchestrion.yml")); err != nil {
		t.Fatal(err)
	}
	if err := CommitAll(ctx, repo, "drop the aspect"); err != nil {
		t.Fatal(err)
	}
	newRef, err := ResolveRef(ctx, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	specs := []*TaskSpec{{
		TaskID:   "t",
		Prompt:   "p",
		Mutation: Mutation{Kind: MutationDeletePaths, Paths: []string{"contrib/thing/orchestrion.yml"}},
	}}

	if err := VerifyMutations(ctx, repo, "", oldRef, specs); err != nil {
		t.Errorf("should apply at the older ref: %v", err)
	}
	err = VerifyMutations(ctx, repo, "", newRef, specs)
	if err == nil {
		t.Fatal("should fail at the newer ref")
	}
	if !strings.Contains(err.Error(), "t") || !strings.Contains(err.Error(), newRef) {
		t.Errorf("error should name the task and the ref, got: %v", err)
	}
}
