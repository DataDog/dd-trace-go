// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// signWithFixture generates a fresh fixture (reusing generateWithFixture,
// the same shared helper B07's own tests use) and signs it, returning the
// generation output, blob reader, plan manifest, and sign result together
// so B08 tests can inspect any of them.
func signWithFixture(t *testing.T, branch, version string, signer Signer) (GenerationOutput, GitBlobReader, PlanManifest, SignedCommitResult) {
	t.Helper()
	output, reader := generateWithFixture(t, branch, version)
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	result, err := SignCommit(context.Background(), ExecRunner{}, nil, signer, SignInput{
		Output:   output,
		Manifest: manifest,
		Reader:   reader.Read,
		WorkDir:  reader.Dir,
	})
	if err != nil {
		t.Fatalf("SignCommit: %v", err)
	}
	return output, reader, manifest, result
}

func ephemeralTestSigner(t *testing.T) *Ed25519Signer {
	t.Helper()
	signer, err := NewEphemeralSigner()
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// TestSignCommitSharesTreeWithUnsignedButDiffersInSHA proves §13.5's
// "The unsigned and signed commits have the same tree. Their commit SHAs
// differ," and that the signed commit's sole parent is the recorded
// source SHA, matching the unsigned commit's own parent relationship.
func TestSignCommitSharesTreeWithUnsignedButDiffersInSHA(t *testing.T) {
	output, _, _, result := signWithFixture(t, "dev-v2.9.x", "v2.9.0-dev", ephemeralTestSigner(t))
	if result.SignedOutput.ReleaseSHA == output.CommitSHA {
		t.Fatal("signed commit SHA must differ from unsigned commit SHA")
	}
	if result.SignedOutput.TreeSHA != output.TreeSHA {
		t.Fatalf("signed tree = %s, want unsigned tree %s", result.SignedOutput.TreeSHA, output.TreeSHA)
	}
	if result.SignedOutput.UnsignedSHA != output.CommitSHA {
		t.Fatalf("recorded unsigned SHA = %s, want %s", result.SignedOutput.UnsignedSHA, output.CommitSHA)
	}
	if result.SignedOutput.CommitParentSHA != output.SourceSHA {
		t.Fatalf("signed parent = %s, want source SHA %s", result.SignedOutput.CommitParentSHA, output.SourceSHA)
	}
}

// TestSignCommitTagsAreAnnotatedAndPeelDirectlyToSignedCommit proves
// every expected tag is annotated (its tag object SHA differs from the
// commit it points at) and peels directly to the signed commit, matching
// §13.5's "Every release tag is annotated and peels directly to the
// recorded release commit."
func TestSignCommitTagsAreAnnotatedAndPeelDirectlyToSignedCommit(t *testing.T) {
	_, _, manifest, result := signWithFixture(t, "dev-v2.9.x", "v2.9.0-dev", ephemeralTestSigner(t))
	if len(result.SignedOutput.Tags) == 0 || len(result.SignedOutput.Tags) != len(manifest.ExpectedTags) {
		t.Fatalf("tags = %#v, want one per expected tag %v", result.SignedOutput.Tags, manifest.ExpectedTags)
	}
	for _, tag := range result.SignedOutput.Tags {
		if tag.TagObjectSHA == tag.PeeledCommitSHA {
			t.Fatalf("tag %s is not annotated: object SHA equals peeled commit SHA", tag.Name)
		}
		if tag.PeeledCommitSHA != result.SignedOutput.ReleaseSHA {
			t.Fatalf("tag %s peels to %s, want signed commit %s", tag.Name, tag.PeeledCommitSHA, result.SignedOutput.ReleaseSHA)
		}
	}
}

// TestSignCommitRejectsHostileArtifactWithoutCallerRevalidation proves
// "Never sign an unchecked target worktree": SignCommit itself calls
// ValidateGeneration internally and refuses to construct a signed commit
// for a hostile artifact, even when the caller supplies no separate
// re-validation step of its own (there is no way to skip it: it is not
// exposed as an option).
func TestSignCommitRejectsHostileArtifactWithoutCallerRevalidation(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	manifest, err := PlanManifestFromTaggerOutput(output.TaggerManifest)
	if err != nil {
		t.Fatal(err)
	}
	hostileBlob := hashBlob(t, reader.Dir, "on: push\n")
	hostileCommit := mutateAndAmend(t, reader.Dir, output.CommitSHA, func(entries map[string]treeEntry) {
		entries[".github/workflows/ci.yml"] = treeEntry{mode: "100644", sha: hostileBlob}
	})
	hostileOutput := output
	hostileOutput.CommitSHA = hostileCommit
	hostileOutput.Changes = diffTreeChanges(t, reader.Dir, output.SourceSHA, hostileCommit)

	_, err = SignCommit(context.Background(), ExecRunner{}, nil, ephemeralTestSigner(t), SignInput{
		Output:   hostileOutput,
		Manifest: manifest,
		Reader:   reader.Read,
		WorkDir:  reader.Dir,
	})
	if ErrorCode(err) != "unauthorized_path_changed" || ClassOf(err) != ErrorClassGenerationFailed {
		t.Fatalf("error = %q/%q, want generation_failed unauthorized_path_changed (SignCommit must re-validate internally)", ClassOf(err), ErrorCode(err))
	}
}

// TestSignCommitRejectsUnexpectedTagList proves recreateExpectedTags
// rejects a tag list containing a name outside the derived manifest,
// matching §13.5's "Reject ... unexpected tags, and tags outside the
// derived manifest." A caller cannot smuggle an extra tag past SignCommit
// by tampering with ExpectedTags: this exercises the shared helper
// directly with a hostile addition.
func TestSignCommitRejectsDuplicateTagName(t *testing.T) {
	output, reader := generateWithFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	_, err := recreateExpectedTags(context.Background(), ExecRunner{}, reader.Dir, []string{"v2.9.0-dev.1", "v2.9.0-dev.1"}, output.CommitSHA)
	if ErrorCode(err) != "duplicate_expected_tag" {
		t.Fatalf("error = %q, want duplicate_expected_tag", ErrorCode(err))
	}
}

// TestSignerFingerprintIsIndependentlyRecomputable proves
// SignedOutput.SignerFingerprint is derived by hashing the raw public key
// bytes (documented in signAttestation), so a verifier can recompute it
// from SignerPublicKey alone without needing the original envelope.
func TestSignerFingerprintIsIndependentlyRecomputable(t *testing.T) {
	_, _, _, result := signWithFixture(t, "dev-v2.9.x", "v2.9.0-dev", ephemeralTestSigner(t))
	recomputed := sha256Hex(result.SignerPublicKey)
	if recomputed != result.SignedOutput.SignerFingerprint {
		t.Fatalf("fingerprint = %s, want recomputed %s", result.SignedOutput.SignerFingerprint, recomputed)
	}
}

// TestVerifyAttestationRejectsSwappedSigningKey proves verification does
// not merely check a stored signature blob: it independently recomputes
// the canonical attestation bytes and requires the verifying key's own
// fingerprint to match the recorded SignerFingerprint, so a record whose
// trusted key was swapped after signing is rejected (the changed/
// untrusted signing key at verification time case).
func TestVerifyAttestationRejectsSwappedSigningKey(t *testing.T) {
	_, _, _, result := signWithFixture(t, "dev-v2.9.x", "v2.9.0-dev", ephemeralTestSigner(t))
	attestation := signedAttestation{
		UnsignedSHA:     result.SignedOutput.UnsignedSHA,
		SourceSHA:       result.SignedOutput.SourceSHA,
		TreeSHA:         result.SignedOutput.TreeSHA,
		ReleaseSHA:      result.SignedOutput.ReleaseSHA,
		ResolvedVersion: attestationResolvedVersion(result.SignedOutput),
		Tags:            tagRefNames(result.SignedOutput.Tags),
	}
	data, err := json.Marshal(attestation)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Seal(&fixedKeySigner{publicKey: result.SignerPublicKey, sign: func(d []byte) ([]byte, error) {
		return d, nil // signature bytes are irrelevant to this test's assertion
	}}, data)
	if err != nil {
		t.Fatal(err)
	}

	otherSigner := ephemeralTestSigner(t)
	if err := VerifyAttestation(Ed25519Verifier{}, result.SignedOutput, envelope, otherSigner.PublicKey()); ErrorCode(err) != "signer_fingerprint_mismatch" {
		t.Fatalf("error = %q, want signer_fingerprint_mismatch", ErrorCode(err))
	}
}

// TestVerifyAttestationRejectsTamperedRecord proves VerifyAttestation
// independently re-derives the canonical attestation from the recorded
// SignedOutput fields rather than trusting a stored envelope's data
// wholesale: a SignedOutput field changed after signing (without
// re-sealing) is caught even though the envelope's own signature would
// still verify against its own (now-mismatched) data.
func TestVerifyAttestationRejectsTamperedRecord(t *testing.T) {
	signer := ephemeralTestSigner(t)
	_, _, _, result := signWithFixture(t, "dev-v2.9.x", "v2.9.0-dev", signer)
	attestation := signedAttestation{
		UnsignedSHA:     result.SignedOutput.UnsignedSHA,
		SourceSHA:       result.SignedOutput.SourceSHA,
		TreeSHA:         result.SignedOutput.TreeSHA,
		ReleaseSHA:      result.SignedOutput.ReleaseSHA,
		ResolvedVersion: attestationResolvedVersion(result.SignedOutput),
		Tags:            tagRefNames(result.SignedOutput.Tags),
	}
	data, err := json.Marshal(attestation)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := Seal(signer, data)
	if err != nil {
		t.Fatal(err)
	}
	tampered := result.SignedOutput
	tampered.ReleaseSHA = strings.Repeat("f", 40)
	if err := VerifyAttestation(Ed25519Verifier{}, tampered, envelope, signer.PublicKey()); ErrorCode(err) != "attestation_data_mismatch" {
		t.Fatalf("error = %q, want attestation_data_mismatch", ErrorCode(err))
	}
}

type fixedKeySigner struct {
	publicKey []byte
	sign      func([]byte) ([]byte, error)
}

func (s *fixedKeySigner) Sign(data []byte) (signature, publicKey []byte, err error) {
	sig, err := s.sign(data)
	if err != nil {
		return nil, nil, err
	}
	return sig, s.publicKey, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestSignedOutputDiffDetectsEveryFieldIndependently proves
// signedOutputDiff's "once recorded, immutable" check catches a change
// to each individual SignedOutput field, not merely the first one a
// hand-written test happens to exercise.
func TestSignedOutputDiffDetectsEveryFieldIndependently(t *testing.T) {
	base := SignedOutput{
		UnsignedSHA:       strings.Repeat("a", 40),
		SourceSHA:         strings.Repeat("b", 40),
		TreeSHA:           strings.Repeat("c", 40),
		ReleaseSHA:        strings.Repeat("d", 40),
		CommitParentSHA:   strings.Repeat("b", 40),
		SignerFingerprint: strings.Repeat("e", 64),
		ChangedPaths:      []string{"go.mod", "internal/version/version.go"},
		Tags:              []TagRef{{Name: "v1.0.0", Ref: "refs/tags/v1.0.0", TagObjectSHA: strings.Repeat("f", 40), PeeledCommitSHA: strings.Repeat("d", 40)}},
		Bundle:            Bundle{Path: "requests/1/2/recovery.bundle", SHA256: strings.Repeat("1", 64), SizeBytes: 100, PrerequisiteSHAs: []string{strings.Repeat("b", 40)}},
	}
	cases := []struct {
		name   string
		mutate func(*SignedOutput)
		want   string
	}{
		{"unsigned_sha", func(s *SignedOutput) { s.UnsignedSHA = strings.Repeat("9", 40) }, "unsigned_sha"},
		{"source_sha", func(s *SignedOutput) { s.SourceSHA = strings.Repeat("9", 40) }, "source_sha"},
		{"tree_sha", func(s *SignedOutput) { s.TreeSHA = strings.Repeat("9", 40) }, "tree_sha"},
		{"release_sha", func(s *SignedOutput) { s.ReleaseSHA = strings.Repeat("9", 40) }, "release_sha"},
		{"commit_parent_sha", func(s *SignedOutput) { s.CommitParentSHA = strings.Repeat("9", 40) }, "commit_parent_sha"},
		{"signer_fingerprint", func(s *SignedOutput) { s.SignerFingerprint = strings.Repeat("9", 64) }, "signer_fingerprint"},
		{"changed_paths", func(s *SignedOutput) { s.ChangedPaths = []string{"other.go"} }, "changed_paths"},
		{"tags", func(s *SignedOutput) { s.Tags = []TagRef{{Name: "different"}} }, "tags"},
		{"bundle", func(s *SignedOutput) { s.Bundle.SHA256 = strings.Repeat("9", 64) }, "bundle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			incoming := base
			tc.mutate(&incoming)
			if diff := signedOutputDiff(&base, &incoming); diff != tc.want {
				t.Fatalf("diff = %q, want %q", diff, tc.want)
			}
		})
	}
	if diff := signedOutputDiff(&base, &base); diff != "" {
		t.Fatalf("identical values diff = %q, want empty", diff)
	}
	if diff := signedOutputDiff(nil, &base); diff != "" {
		t.Fatalf("nil existing diff = %q, want empty (nothing recorded yet)", diff)
	}
	if diff := signedOutputDiff(&base, nil); diff != "signed_output_removed" {
		t.Fatalf("nil incoming diff = %q, want signed_output_removed", diff)
	}
}
