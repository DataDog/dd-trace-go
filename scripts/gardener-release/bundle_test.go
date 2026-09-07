// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// bundleFixture builds a source remote, generates and signs a fresh
// artifact, and returns everything needed to build and later restore a
// recovery bundle: the source remote path (still available after the
// generating workdir is discarded), the sign result, and the generating
// workdir path itself (callers must discard/remove this before testing
// recovery).
func bundleFixture(t *testing.T, branch, version string) (sourceRemotePath string, output GenerationOutput, result SignedCommitResult, generatingWorkDir string) {
	t.Helper()
	remotePath, sourceSHA := sourceFixtureRepo(t, branch)
	workDir := t.TempDir()
	genOutput, err := Generate(context.Background(), ExecRunner{}, GenerateInput{
		SourceRemotePath: remotePath,
		SourceSHA:        sourceSHA,
		TargetBranch:     branch,
		ResolvedVersion:  version,
		UntaggedModules:  []string{"example.com/root/moduleC/v2"},
		TaggerBinaryPath: buildTaggerBinary(t),
		WorkDir:          workDir,
		CacheDirs:        isolatedCacheDirs(t),
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	manifest, err := PlanManifestFromTaggerOutput(genOutput.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	reader := GitBlobReader{Runner: ExecRunner{}, Dir: workDir}
	signResult, err := SignCommit(context.Background(), ExecRunner{}, nil, ephemeralTestSigner(t), SignInput{
		Output:   genOutput,
		Manifest: manifest,
		Reader:   reader.Read,
		WorkDir:  workDir,
	})
	if err != nil {
		t.Fatalf("SignCommit: %v", err)
	}
	return remotePath, genOutput, signResult, workDir
}

// TestBuildRecoveryBundleContainsExactlyReleaseCommitAndTags proves
// BuildRecoveryBundle's own independent verification: the bundle's ref
// set is exactly the signed commit plus every tag, no extras, and its
// sole prerequisite is the recorded source SHA (a genuinely thin bundle,
// not a full pack).
func TestBuildRecoveryBundleContainsExactlyReleaseCommitAndTags(t *testing.T) {
	_, output, result, workDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(context.Background(), ExecRunner{}, BuildRecoveryBundleInput{
		WorkDir:    workDir,
		SourceSHA:  output.SourceSHA,
		ReleaseSHA: result.SignedOutput.ReleaseSHA,
		Tags:       result.SignedOutput.Tags,
		OutputPath: bundlePath,
	})
	if err != nil {
		t.Fatalf("BuildRecoveryBundle: %v", err)
	}
	if bundle.Path != bundlePath || bundle.SHA256 == "" || bundle.SizeBytes <= 0 {
		t.Fatalf("unexpected bundle: %#v", bundle)
	}
	if len(bundle.PrerequisiteSHAs) != 1 || bundle.PrerequisiteSHAs[0] != output.SourceSHA {
		t.Fatalf("prerequisites = %v, want exactly [%s] (a thin bundle)", bundle.PrerequisiteSHAs, output.SourceSHA)
	}
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256Hex(data)
	if sum != bundle.SHA256 {
		t.Fatalf("recorded digest = %s, want independently recomputed %s", bundle.SHA256, sum)
	}
}

// TestBuildRecoveryBundleFixtureStaysUnderSizeCap proves a real fixture
// bundle's actual on-disk size is read and stays comfortably under
// §13.2's 50 MiB cap in the ordinary case (the cap-exceeded case itself
// is covered directly against checkRecoveryBundleSizeCap in
// TestCheckRecoveryBundleSizeCapBoundary, since inflating a real Git
// bundle past 50 MiB is impractical in a unit test).
func TestBuildRecoveryBundleFixtureStaysUnderSizeCap(t *testing.T) {
	_, output, result, workDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(context.Background(), ExecRunner{}, BuildRecoveryBundleInput{
		WorkDir:    workDir,
		SourceSHA:  output.SourceSHA,
		ReleaseSHA: result.SignedOutput.ReleaseSHA,
		Tags:       result.SignedOutput.Tags,
		OutputPath: bundlePath,
	})
	if err != nil {
		t.Fatalf("BuildRecoveryBundle: %v", err)
	}
	if bundle.SizeBytes <= 0 || bundle.SizeBytes >= MaxRecoveryBundleBytes {
		t.Fatalf("bundle size = %d, want a small positive size under the cap", bundle.SizeBytes)
	}
}

// TestCheckRecoveryBundleSizeCapBoundary proves §13.2's "Recovery bundle:
// At most 50 MiB. Stop before publishing if the bundle exceeds the cap"
// at the exact limit and one byte above it, matching §13.2's own
// instruction to "Test every boundary at the limit and one unit above
// it."
func TestCheckRecoveryBundleSizeCapBoundary(t *testing.T) {
	if err := checkRecoveryBundleSizeCap(MaxRecoveryBundleBytes); err != nil {
		t.Fatalf("at the exact cap: %v, want no error", err)
	}
	if err := checkRecoveryBundleSizeCap(MaxRecoveryBundleBytes + 1); ErrorCode(err) != "bundle_exceeds_size_cap" {
		t.Fatalf("one byte over the cap: error = %q, want bundle_exceeds_size_cap", ErrorCode(err))
	}
}

// TestRecoveryRestoresExactObjectsAfterGeneratingWorkDirDeleted is B08's
// core "Prove" requirement: build the bundle from a generating clone,
// delete/discard that entire clone directory (verified below to
// literally no longer exist, not merely unreferenced), then restore into
// a brand-new empty clone using only the bundle plus the recorded source
// SHA fetched from the still-available source remote, and assert every
// recovered commit/tree/tag SHA is byte-identical to what was recorded.
func TestRecoveryRestoresExactObjectsAfterGeneratingWorkDirDeleted(t *testing.T) {
	sourceRemotePath, output, result, workDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(context.Background(), ExecRunner{}, BuildRecoveryBundleInput{
		WorkDir:    workDir,
		SourceSHA:  output.SourceSHA,
		ReleaseSHA: result.SignedOutput.ReleaseSHA,
		Tags:       result.SignedOutput.Tags,
		OutputPath: bundlePath,
	})
	if err != nil {
		t.Fatalf("BuildRecoveryBundle: %v", err)
	}

	// Literally delete the generating clone: not merely stop referencing
	// it, but remove the directory from disk, then assert it is gone.
	// This is the only way "the original clone and transient artifacts
	// are removed" (P05/B08's Prove) can be genuinely exercised rather
	// than accidentally satisfied by files still present on disk.
	if err := os.RemoveAll(workDir); err != nil {
		t.Fatalf("remove generating workdir: %v", err)
	}
	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Fatalf("generating workdir still exists after removal: stat err=%v", err)
	}

	freshWorkDir := t.TempDir()
	err = RestoreFromBundle(context.Background(), ExecRunner{}, RestoreFromBundleInput{
		FreshWorkDir:       freshWorkDir,
		SourceRemotePath:   sourceRemotePath,
		SourceSHA:          output.SourceSHA,
		BundlePath:         bundlePath,
		ExpectedBundle:     bundle,
		ExpectedReleaseSHA: result.SignedOutput.ReleaseSHA,
		ExpectedTreeSHA:    result.SignedOutput.TreeSHA,
		ExpectedTags:       result.SignedOutput.Tags,
	})
	if err != nil {
		t.Fatalf("RestoreFromBundle: %v", err)
	}

	// Independently re-derive the recovered commit/tree/tag SHAs from the
	// fresh clone itself (not merely trusting RestoreFromBundle's nil
	// error), proving the recovered objects are genuinely present and
	// byte-identical to what was recorded.
	commitSHA := runGit(t, freshWorkDir, "rev-parse", result.SignedOutput.ReleaseSHA)
	if trimmed := trimNewline(commitSHA); trimmed != result.SignedOutput.ReleaseSHA {
		t.Fatalf("recovered commit = %s, want %s", trimmed, result.SignedOutput.ReleaseSHA)
	}
	treeSHA := runGit(t, freshWorkDir, "rev-parse", result.SignedOutput.ReleaseSHA+"^{tree}")
	if trimmed := trimNewline(treeSHA); trimmed != result.SignedOutput.TreeSHA {
		t.Fatalf("recovered tree = %s, want %s", trimmed, result.SignedOutput.TreeSHA)
	}
	for _, tag := range result.SignedOutput.Tags {
		tagObjectSHA := trimNewline(runGit(t, freshWorkDir, "rev-parse", tag.Ref))
		if tagObjectSHA != tag.TagObjectSHA {
			t.Fatalf("recovered tag object %s = %s, want %s", tag.Name, tagObjectSHA, tag.TagObjectSHA)
		}
		peeledSHA := trimNewline(runGit(t, freshWorkDir, "rev-parse", tag.Ref+"^{commit}"))
		if peeledSHA != tag.PeeledCommitSHA {
			t.Fatalf("recovered peeled tag %s = %s, want %s", tag.Name, peeledSHA, tag.PeeledCommitSHA)
		}
	}
}

// TestRestoreFromBundleS05MissingPrerequisite proves recovery stops when
// the recorded source SHA is not reachable from the source remote (the
// prerequisite is unavailable), rather than regenerating or substituting
// a newer source.
func TestRestoreFromBundleS05MissingPrerequisite(t *testing.T) {
	sourceRemotePath, output, result, workDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(context.Background(), ExecRunner{}, BuildRecoveryBundleInput{
		WorkDir:    workDir,
		SourceSHA:  output.SourceSHA,
		ReleaseSHA: result.SignedOutput.ReleaseSHA,
		Tags:       result.SignedOutput.Tags,
		OutputPath: bundlePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(workDir)

	// A different, empty bare remote stands in for "the recorded source
	// commit is not reachable from an approved protected ref."
	emptyRemote := newBareFixtureRemote(t)
	freshWorkDir := t.TempDir()
	err = RestoreFromBundle(context.Background(), ExecRunner{}, RestoreFromBundleInput{
		FreshWorkDir:       freshWorkDir,
		SourceRemotePath:   emptyRemote,
		SourceSHA:          output.SourceSHA,
		BundlePath:         bundlePath,
		ExpectedBundle:     bundle,
		ExpectedReleaseSHA: result.SignedOutput.ReleaseSHA,
		ExpectedTreeSHA:    result.SignedOutput.TreeSHA,
		ExpectedTags:       result.SignedOutput.Tags,
	})
	if err == nil {
		t.Fatal("expected error for missing prerequisite")
	}
	if ErrorCode(err) != "recovery_prerequisite_fetch_failed" && ErrorCode(err) != "recovery_prerequisite_unreachable" {
		t.Fatalf("error = %q, want a recovery_prerequisite_* code", ErrorCode(err))
	}
	_ = sourceRemotePath
}

// TestRestoreFromBundleS05WrongDigest proves a bundle whose recorded
// SHA256 does not match its actual bytes is rejected before any Git
// operation runs against it.
func TestRestoreFromBundleS05WrongDigest(t *testing.T) {
	sourceRemotePath, output, result, workDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(context.Background(), ExecRunner{}, BuildRecoveryBundleInput{
		WorkDir:    workDir,
		SourceSHA:  output.SourceSHA,
		ReleaseSHA: result.SignedOutput.ReleaseSHA,
		Tags:       result.SignedOutput.Tags,
		OutputPath: bundlePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(workDir)
	bundle.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"

	freshWorkDir := t.TempDir()
	err = RestoreFromBundle(context.Background(), ExecRunner{}, RestoreFromBundleInput{
		FreshWorkDir:       freshWorkDir,
		SourceRemotePath:   sourceRemotePath,
		SourceSHA:          output.SourceSHA,
		BundlePath:         bundlePath,
		ExpectedBundle:     bundle,
		ExpectedReleaseSHA: result.SignedOutput.ReleaseSHA,
		ExpectedTreeSHA:    result.SignedOutput.TreeSHA,
		ExpectedTags:       result.SignedOutput.Tags,
	})
	if ErrorCode(err) != "bundle_digest_mismatch" {
		t.Fatalf("error = %q, want bundle_digest_mismatch", ErrorCode(err))
	}
}

// TestRestoreFromBundleS05TruncatedBundle proves a bundle file truncated
// after creation (its digest and size no longer match) is rejected.
func TestRestoreFromBundleS05TruncatedBundle(t *testing.T) {
	sourceRemotePath, output, result, workDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(context.Background(), ExecRunner{}, BuildRecoveryBundleInput{
		WorkDir:    workDir,
		SourceSHA:  output.SourceSHA,
		ReleaseSHA: result.SignedOutput.ReleaseSHA,
		Tags:       result.SignedOutput.Tags,
		OutputPath: bundlePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(workDir)
	if err := os.Truncate(bundlePath, bundle.SizeBytes/2); err != nil {
		t.Fatal(err)
	}

	freshWorkDir := t.TempDir()
	err = RestoreFromBundle(context.Background(), ExecRunner{}, RestoreFromBundleInput{
		FreshWorkDir:       freshWorkDir,
		SourceRemotePath:   sourceRemotePath,
		SourceSHA:          output.SourceSHA,
		BundlePath:         bundlePath,
		ExpectedBundle:     bundle,
		ExpectedReleaseSHA: result.SignedOutput.ReleaseSHA,
		ExpectedTreeSHA:    result.SignedOutput.TreeSHA,
		ExpectedTags:       result.SignedOutput.Tags,
	})
	if ErrorCode(err) != "bundle_digest_mismatch" && ErrorCode(err) != "bundle_size_mismatch" {
		t.Fatalf("error = %q, want bundle_digest_mismatch or bundle_size_mismatch", ErrorCode(err))
	}
}

// TestRestoreFromBundleS05ExtraBundleRefs proves a bundle containing a
// ref beyond the expected signed commit and its tags is rejected by
// verifyBundleContents's exact-set comparison, even though the bundle
// itself is otherwise well-formed and verifiable by `git bundle verify`.
func TestRestoreFromBundleS05ExtraBundleRefs(t *testing.T) {
	sourceRemotePath, output, result, workDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")

	// Build a hostile bundle directly (not through BuildRecoveryBundle,
	// which would itself reject the extra ref) containing an additional
	// branch beyond the release commit and its tags. The extra ref must
	// point at a commit with content not already reachable from
	// SourceSHA, or `git bundle create` silently omits it (nothing new to
	// bundle); an orphan commit guarantees that.
	runGit(t, workDir, "branch", "--force", "gardener-release-bundle-tip", result.SignedOutput.ReleaseSHA)
	hostileCommit := trimNewline(runGitWithIdentity(t, workDir, "commit-tree", result.SignedOutput.TreeSHA, "-m", "orphan hostile commit"))
	runGit(t, workDir, "branch", "--force", "extra-hostile-ref", hostileCommit)
	args := []string{"bundle", "create", bundlePath, "refs/heads/gardener-release-bundle-tip", "refs/heads/extra-hostile-ref"}
	for _, tag := range result.SignedOutput.Tags {
		args = append(args, tag.Ref)
	}
	args = append(args, "^"+output.SourceSHA)
	runGit(t, workDir, args...)
	data, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	bundle := Bundle{Path: bundlePath, SHA256: sha256Hex(data), SizeBytes: int64(len(data)), PrerequisiteSHAs: []string{output.SourceSHA}}
	os.RemoveAll(workDir)

	freshWorkDir := t.TempDir()
	err = RestoreFromBundle(context.Background(), ExecRunner{}, RestoreFromBundleInput{
		FreshWorkDir:       freshWorkDir,
		SourceRemotePath:   sourceRemotePath,
		SourceSHA:          output.SourceSHA,
		BundlePath:         bundlePath,
		ExpectedBundle:     bundle,
		ExpectedReleaseSHA: result.SignedOutput.ReleaseSHA,
		ExpectedTreeSHA:    result.SignedOutput.TreeSHA,
		ExpectedTags:       result.SignedOutput.Tags,
	})
	if ErrorCode(err) != "bundle_unexpected_refs" {
		t.Fatalf("error = %q, want bundle_unexpected_refs", ErrorCode(err))
	}
}

// TestRestoreFromBundleS05ChangedSigningKeyAtVerification proves that
// even though RestoreFromBundle itself does not check a signing key (the
// bundle contains Git objects, not the content-layer signature), the
// paired VerifyAttestation call a real resume path would make against a
// changed/untrusted key is independently rejected: this composes
// RestoreFromBundle's object-recovery proof with sign_test.go's
// TestVerifyAttestationRejectsSwappedSigningKey coverage to show both
// halves must pass.
func TestRestoreFromBundleS05ChangedSigningKeyAtVerification(t *testing.T) {
	sourceRemotePath, output, result, workDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), "recovery.bundle")
	bundle, err := BuildRecoveryBundle(context.Background(), ExecRunner{}, BuildRecoveryBundleInput{
		WorkDir:    workDir,
		SourceSHA:  output.SourceSHA,
		ReleaseSHA: result.SignedOutput.ReleaseSHA,
		Tags:       result.SignedOutput.Tags,
		OutputPath: bundlePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(workDir)

	freshWorkDir := t.TempDir()
	if err := RestoreFromBundle(context.Background(), ExecRunner{}, RestoreFromBundleInput{
		FreshWorkDir:       freshWorkDir,
		SourceRemotePath:   sourceRemotePath,
		SourceSHA:          output.SourceSHA,
		BundlePath:         bundlePath,
		ExpectedBundle:     bundle,
		ExpectedReleaseSHA: result.SignedOutput.ReleaseSHA,
		ExpectedTreeSHA:    result.SignedOutput.TreeSHA,
		ExpectedTags:       result.SignedOutput.Tags,
	}); err != nil {
		t.Fatalf("RestoreFromBundle: %v", err)
	}
	untrustedSigner := ephemeralTestSigner(t)
	envelope, err := Seal(untrustedSigner, mustMarshalAttestationForTest(t, result.SignedOutput))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAttestation(Ed25519Verifier{}, result.SignedOutput, envelope, untrustedSigner.PublicKey()); ErrorCode(err) != "signer_fingerprint_mismatch" {
		t.Fatalf("error = %q, want signer_fingerprint_mismatch", ErrorCode(err))
	}
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func mustMarshalAttestationForTest(t *testing.T, signed SignedOutput) []byte {
	t.Helper()
	attestation := signedAttestation{
		UnsignedSHA:     signed.UnsignedSHA,
		SourceSHA:       signed.SourceSHA,
		TreeSHA:         signed.TreeSHA,
		ReleaseSHA:      signed.ReleaseSHA,
		ResolvedVersion: attestationResolvedVersion(signed),
		Tags:            tagRefNames(signed.Tags),
	}
	data, err := json.Marshal(attestation)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
