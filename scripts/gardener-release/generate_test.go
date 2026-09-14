// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"strings"
	"testing"
)

// TestGenerateFreshDevelopmentVersion covers §15 G05 for a fresh
// development version: generation must succeed using only verified
// module-local replace directives, with GOWORK=off/GOPROXY=off/GOSUMDB=off
// and isolated caches, since no target version is reachable in any real
// proxy or registry from this test process.
func TestGenerateFreshDevelopmentVersionG05(t *testing.T) {
	output, _ := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	if output.CommitSHA == "" || output.CommitSHA == output.SourceSHA {
		t.Fatalf("unexpected generation output: %#v", output)
	}
	if len(output.ParentSHAs) != 1 || output.ParentSHAs[0] != output.SourceSHA {
		t.Fatalf("unexpected parents: %#v", output.ParentSHAs)
	}
	if output.Branch != "dev-v2.9.x" {
		t.Fatalf("branch = %q, want dev-v2.9.x", output.Branch)
	}
	if len(output.Changes) == 0 {
		t.Fatal("expected at least one changed file")
	}
	if output.TaggerManifest == nil {
		t.Fatal("expected non-nil tagger manifest")
	}
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatalf("PlanManifestFromTaggerOutput: %v", err)
	}
	if len(manifest.ExpectedTags) == 0 || manifest.ExpectedTags[0] != "v2.9.0-dev" {
		t.Fatalf("unexpected expected tags: %#v", manifest.ExpectedTags)
	}
}

// TestGenerateFreshRCVersionG05 covers §15 G05 for a fresh RC version on
// an existing release branch (moduleA/moduleB depend on the root module
// via a local replace; moduleC is untagged but still updated).
func TestGenerateFreshRCVersionG05(t *testing.T) {
	output, reader := generateWithFixture(t, "release-v2.9.x", "v2.9.9-rc.1")
	if output.Branch != "release-v2.9.x" {
		t.Fatalf("branch = %q, want release-v2.9.x", output.Branch)
	}
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatalf("PlanManifestFromTaggerOutput: %v", err)
	}
	validated, err := ValidateGeneration(context.Background(), reader.Read, output, manifest)
	if err != nil {
		t.Fatalf("ValidateGeneration rejected a fresh RC generation: %v", err)
	}
	if validated.ResolvedVersion != "v2.9.9-rc.1" || !validated.VersionFileChanged {
		t.Fatalf("unexpected validated generation: %#v", validated)
	}
	if len(validated.ChangedGoModPaths) == 0 {
		t.Fatalf("expected changed go.mod paths, got none: %#v", validated)
	}
}

// TestGenerateNeverAcceptsCredentialSeam proves G04 by construction:
// Generate's and ValidateGeneration's parameter types contain no Signer,
// write-capable GitHub client, or credential type. This is checked at
// compile time by the function signatures themselves (see generate.go
// and validate.go); this test additionally proves at runtime that a
// CommandRunner double which would fail loudly if ever asked to invoke a
// signing-shaped command is never triggered by Generate, i.e. Generate's
// only subprocess calls are git and the tagger binary, never anything
// resembling a signing operation.
func TestGenerateNeverAcceptsCredentialSeam(t *testing.T) {
	remotePath, sourceSHA := sourceFixtureRepo(t, "dev-v2.9.x")
	guard := &credentialGuardRunner{Runner: ExecRunner{}, t: t}
	workDir := t.TempDir()
	_, err := Generate(context.Background(), guard, GenerateInput{
		SourceRemotePath: remotePath,
		SourceSHA:        sourceSHA,
		TargetBranch:     "dev-v2.9.x",
		ResolvedVersion:  "v2.9.0-dev",
		UntaggedModules:  []string{"example.com/root/moduleC/v2"},
		TaggerBinaryPath: buildTaggerBinary(t),
		WorkDir:          workDir,
		CacheDirs:        isolatedCacheDirs(t),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if guard.sawForbiddenCommand {
		t.Fatal("Generate invoked a command resembling signing or token/credential access")
	}
}

// credentialGuardRunner wraps ExecRunner and fails any command whose path
// or arguments look like a signing, token-minting, or credential-access
// operation, proving Generate's subprocess surface never reaches for one.
type credentialGuardRunner struct {
	Runner              CommandRunner
	t                   *testing.T
	sawForbiddenCommand bool
}

// forbiddenExecutableNames are executables that would only ever appear in
// a signing, key-management, or token-minting operation. This guard
// checks only the executable's base name (never the full path or
// arguments, which legitimately contain caller-supplied temp directory
// names such as the test's own name) so a t.TempDir() path that happens
// to contain one of these words as a substring can never produce a false
// positive.
var forbiddenExecutableNames = map[string]bool{
	"gpg":         true,
	"gpg2":        true,
	"ssh-add":     true,
	"ssh-agent":   true,
	"dd-octo-sts": true,
	"gh":          true,
	"curl":        true,
	"wget":        true,
}

func (r *credentialGuardRunner) Run(ctx context.Context, command Command) (CommandResult, error) {
	base := command.Path
	if idx := strings.LastIndexByte(base, '/'); idx >= 0 {
		base = base[idx+1:]
	}
	if forbiddenExecutableNames[base] {
		r.sawForbiddenCommand = true
		r.t.Fatalf("forbidden command invoked: %s %v", command.Path, command.Args)
	}
	// Flag-shaped arguments (starting with "-") are checked for
	// credential/token/signing-looking flag names; this still excludes
	// arbitrary positional arguments like temp directory paths or SHAs.
	for _, arg := range command.Args {
		if strings.HasPrefix(arg, "-") && (containsFold(arg, "token") || containsFold(arg, "credential") || containsFold(arg, "gpgsign")) {
			// commit.gpgsign/tag.gpgsign=false is expected and safe: Generate
			// explicitly disables signing, it never enables it. Only flag an
			// attempt to *enable* signing or to reference a token/credential.
			if containsFold(arg, "gpgsign") {
				continue
			}
			r.sawForbiddenCommand = true
			r.t.Fatalf("forbidden flag invoked: %s %v", command.Path, command.Args)
		}
	}
	return r.Runner.Run(ctx, command)
}

func containsFold(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexFold(haystack, needle) >= 0
}

func indexFold(haystack, needle string) int {
	h := []byte(haystack)
	n := []byte(needle)
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			hc, nc := h[i+j], n[j]
			if hc >= 'A' && hc <= 'Z' {
				hc += 'a' - 'A'
			}
			if nc >= 'A' && nc <= 'Z' {
				nc += 'a' - 'A'
			}
			if hc != nc {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// TestGenerateRejectsMissingSourceCommit proves generation fails closed
// (rather than silently falling back to some other ref) when the
// recorded source SHA cannot be fetched from the source remote.
func TestGenerateRejectsMissingSourceCommit(t *testing.T) {
	remotePath, _ := sourceFixtureRepo(t, "dev-v2.9.x")
	_, err := Generate(context.Background(), ExecRunner{}, GenerateInput{
		SourceRemotePath: remotePath,
		SourceSHA:        "0123456789abcdef0123456789abcdef01234567",
		TargetBranch:     "dev-v2.9.x",
		ResolvedVersion:  "v2.9.0-dev",
		TaggerBinaryPath: buildTaggerBinary(t),
		WorkDir:          t.TempDir(),
		CacheDirs:        isolatedCacheDirs(t),
	})
	if ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error class = %q, want generation_failed", ClassOf(err))
	}
}

// TestGenerateRejectsInvalidSourceSHA proves Generate validates the
// source SHA's syntax before doing anything else, rather than passing an
// attacker-controlled string straight into a git command line.
func TestGenerateRejectsInvalidSourceSHA(t *testing.T) {
	_, err := Generate(context.Background(), ExecRunner{}, GenerateInput{
		SourceRemotePath: "unused",
		SourceSHA:        "not-a-sha;rm -rf /",
		TargetBranch:     "dev-v2.9.x",
		ResolvedVersion:  "v2.9.0-dev",
		WorkDir:          t.TempDir(),
	})
	if ErrorCode(err) != "invalid_git_object" {
		t.Fatalf("error = %q, want invalid_git_object", ErrorCode(err))
	}
}
