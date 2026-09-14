// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProtectedGenerationRederivesGitChangesBeforeSigning(t *testing.T) {
	remote, source := sourceFixtureRepo(t, "dev-v2.9.x")
	workDir := t.TempDir()
	tagger := buildTaggerBinary(t)
	output, err := Generate(context.Background(), ExecRunner{}, GenerateInput{
		SourceRemotePath: remote,
		SourceSHA:        source,
		TargetBranch:     "dev-v2.9.x",
		ResolvedVersion:  "v2.9.0-dev",
		TaggerBinaryPath: tagger,
		WorkDir:          workDir,
		CacheDirs:        isolatedCacheDirs(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	wantManifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rederiveProtectedGeneration(context.Background(), ExecRunner{}, workDir, tagger, output); err != nil || !reflect.DeepEqual(got, wantManifest) {
		t.Fatalf("trusted re-derivation failed: got=%#v err=%v", got, err)
	}
	runGit(t, workDir, "checkout", "--detach", output.CommitSHA)
	if err := os.MkdirAll(filepath.Join(workDir, ".github", "workflows"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, ".github", "workflows", "evil.yml"), []byte("name: evil\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, workDir, "add", "-f", ".github/workflows/evil.yml")
	runGit(t, workDir, "commit", "--amend", "--no-edit")
	output.CommitSHA = strings.TrimSpace(runGit(t, workDir, "rev-parse", "HEAD"))
	output.TreeSHA = strings.TrimSpace(runGit(t, workDir, "rev-parse", "HEAD^{tree}"))
	// Keep the original claimed Changes and manifest. The protected reader
	// must reject the hidden workflow path from Git before any signer exists.
	if _, err := rederiveProtectedGeneration(context.Background(), ExecRunner{}, workDir, tagger, output); ErrorCode(err) != "generation_git_claim_mismatch" {
		t.Fatalf("error = %q, want generation_git_claim_mismatch", ErrorCode(err))
	}
}

func TestProtectedCompositionRejectsConcealedMaliciousBundleBeforeSignOrPersist(t *testing.T) {
	remote, source := sourceFixtureRepo(t, "dev-v2.9.x")
	generationDir := t.TempDir()
	tagger := buildTaggerBinary(t)
	output, err := Generate(context.Background(), ExecRunner{}, GenerateInput{
		SourceRemotePath: remote,
		SourceSHA:        source,
		TargetBranch:     "dev-v2.9.x",
		ResolvedVersion:  "v2.9.0-dev",
		TaggerBinaryPath: tagger,
		WorkDir:          generationDir,
		CacheDirs:        isolatedCacheDirs(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, generationDir, "checkout", "--detach", output.CommitSHA)
	if err := os.MkdirAll(filepath.Join(generationDir, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generationDir, ".github", "workflows", "evil.yml"), []byte("name: evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, generationDir, "add", "-f", ".github/workflows/evil.yml")
	runGit(t, generationDir, "commit", "--amend", "--no-edit")
	malicious := output
	malicious.CommitSHA = strings.TrimSpace(runGit(t, generationDir, "rev-parse", "HEAD"))
	malicious.TreeSHA = strings.TrimSpace(runGit(t, generationDir, "rev-parse", "HEAD^{tree}"))
	// Keep Changes and TaggerManifest from the benign output to conceal the path.
	bundlePath := filepath.Join(t.TempDir(), UnsignedRepositoryBundleName)
	bundleDigest, err := BuildUnsignedRepositoryBundle(context.Background(), ExecRunner{}, generationDir, bundlePath, malicious)
	if err != nil {
		t.Fatal(err)
	}
	protectedDir := t.TempDir()
	runGit(t, protectedDir, "init", "--quiet", "-b", "protected")
	runGit(t, protectedDir, "fetch", "--quiet", "--no-tags", remote, source)
	signCalled, persistCalled := false, false
	_, err = validateProtectedGenerationAndStore(context.Background(), ExecRunner{}, protectedDir, bundlePath, bundleDigest, tagger, malicious, manifest, protectedSignStoreActions{
		sign: func(PlanManifest) (SignedCommitResult, error) {
			signCalled = true
			return SignedCommitResult{}, nil
		},
		persist: func(SignedCommitResult) (ProductionSignResult, error) {
			persistCalled = true
			return ProductionSignResult{}, nil
		},
	})
	if ErrorCode(err) != "generation_git_claim_mismatch" {
		t.Fatalf("error = %q, want generation_git_claim_mismatch", ErrorCode(err))
	}
	if signCalled || persistCalled {
		t.Fatalf("malicious artifact reached sign=%t persist=%t", signCalled, persistCalled)
	}
}

func TestPlanManifestStrictSchemaBranchAndFields(t *testing.T) {
	base := map[string]any{
		"schema_version": "1", "source_sha": string(make([]byte, 40)), "branch": "dev-v2.9.x", "requested_version": "v2.9.0-dev", "root_module": "example.com/root",
		"modules":                []any{map[string]any{"path": "example.com/root", "dir": ".", "tagged": true}},
		"permitted_output_files": []any{"go.mod"}, "expected_tags": []any{"v2.9.0-dev"},
	}
	for name, mutate := range map[string]func(map[string]any){
		"schema":  func(v map[string]any) { v["schema_version"] = "2" },
		"branch":  func(v map[string]any) { v["branch"] = "feature/untrusted" },
		"unknown": func(v map[string]any) { v["extra"] = true },
	} {
		t.Run(name, func(t *testing.T) {
			copy := make(map[string]any, len(base)+1)
			for key, value := range base {
				copy[key] = value
			}
			mutate(copy)
			if _, err := PlanManifestFromTaggerOutput(copy); err == nil {
				t.Fatal("malformed manifest accepted")
			}
		})
	}
}
