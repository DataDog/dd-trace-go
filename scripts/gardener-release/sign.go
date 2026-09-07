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
)

// signingCommitterName/Email are the fixed, non-secret Git identity used
// for the *signed* commit object, distinct from generate.go's
// generationCommitterName/Email used for the *unsigned* commit. Using a
// different committer identity (and a fresh author date at signing time)
// is what makes the signed and unsigned commit SHAs differ even though
// §13.5 requires them to share one tree: "The unsigned and signed commits
// have the same tree. Their commit SHAs differ."
const (
	signingCommitterName  = "gardener-release-sign"
	signingCommitterEmail = "gardener-release-sign@datadoghq.invalid"
)

// SignInput is everything SignCommit needs to re-validate a generation
// and construct its signed commit and tags. It carries no Signer,
// write-capable GitHub client, or any token/credential type beyond the
// injected Signer parameter to SignCommit itself, which callers supply
// separately (see SignCommit's signature) so this struct alone can never
// satisfy §13.6's "no signing environment or write token" for the
// read-only validate/inspect job group.
type SignInput struct {
	// Output and Manifest are re-validated by SignCommit itself: see
	// SignCommit's doc comment. Never trust a caller's claim that these
	// already passed ValidateGeneration in an earlier job.
	Output   GenerationOutput
	Manifest PlanManifest
	Reader   modFileReader
	// WorkDir is the Git repository containing both Output.SourceSHA and
	// Output.CommitSHA (the checkout Generate produced). SignCommit reads
	// and writes Git objects here; it never checks out a working tree.
	WorkDir string
}

// SignedCommitResult is SignCommit's success output: everything needed to
// populate state.go's SignedOutput, plus the intermediate values a
// recovery bundle build needs.
//
// SignedCommitResult.SignedOutput.ToolDigest and .ValidatorDigest are
// deliberately left unset ($13.5 requires both, but as fields of the
// eventual persisted record, not necessarily as outputs this function
// alone can produce). SignCommit only has a WorkDir and the already-built
// TaggerBinaryPath a caller passed to Generate; it has no artifact-fetch
// context of its own. Per $13.6, validating "the source run ID, attempt,
// artifact name, byte digest, and declared schema" of a downloaded
// generation artifact is the calling protected job's responsibility, not
// this wrapper function's -- the same layering state.go already uses for
// Record.WorkflowSHA/Record.ToolSHA, which are also populated by the
// caller, not derived here. The caller (eventually B12's workflow
// assembly) must set ToolDigest/ValidatorDigest on the returned
// SignedOutput before persisting the `signed` record through
// AdvanceToSigned/PersistReservation; leaving them empty is a caller
// contract, not evidence that they are optional.
type SignedCommitResult struct {
	SignedOutput SignedOutput
	// SignerPublicKey is the raw public key bytes used, so a caller that
	// wants to persist or log it separately from the fingerprint can (the
	// fingerprint alone is a one-way digest; state.go's SignedOutput only
	// stores the fingerprint, per §13.5).
	SignerPublicKey []byte
}

// SignCommit re-validates the generation, constructs the signed commit
// sharing the validated tree and recorded parent with the unsigned
// commit, verifies the constructed commit by reading it back through Git
// plumbing, recreates every expected tag at the signed commit, and
// returns bounded evidence. It never trusts a caller's earlier
// ValidateGeneration call: "Never sign an unchecked target worktree"
// means this function's only path to constructing a signed commit is
// through calling ValidateGeneration itself, right here, against the
// artifact it is about to sign.
//
// SignCommit never pushes, never touches a real remote, and never
// accepts a write-capable GitHub client: its only I/O is CommandRunner
// calls against input.WorkDir, a local checkout the caller already
// produced with Generate.
func SignCommit(ctx context.Context, runner CommandRunner, clock Clock, signer Signer, input SignInput) (SignedCommitResult, error) {
	validated, err := ValidateGeneration(ctx, input.Reader, input.Output, input.Manifest)
	if err != nil {
		return SignedCommitResult{}, err
	}
	if signer == nil {
		return SignedCommitResult{}, newReleaseError(ErrorClassEvidenceIncomplete, "signer_unavailable")
	}
	if clock == nil {
		clock = RealClock{}
	}
	parentSHA := ""
	if len(input.Output.ParentSHAs) > 0 {
		parentSHA = input.Output.ParentSHAs[0]
	}

	message := "release: " + validated.ResolvedVersion
	authorDate := clock.Now().Unix()
	command, err := BuildCommitTreeCommand("git", gitStateEnv(), input.WorkDir, validated.TreeSHA, parentSHA, message, signingCommitterName, signingCommitterEmail, authorDate)
	if err != nil {
		return SignedCommitResult{}, err
	}
	result, err := runner.Run(ctx, command)
	if err != nil {
		return SignedCommitResult{}, wrapReleaseError(ErrorClassGenerationFailed, "signing_commit_failed", err)
	}
	signedSHA := strings.TrimSpace(result.Stdout)
	if !ValidGitObjectID(signedSHA) {
		return SignedCommitResult{}, newReleaseError(ErrorClassGenerationFailed, "signing_commit_malformed")
	}

	// Never trust the subprocess's exit code alone as proof: read the
	// constructed commit back and assert its tree/parent/message/author
	// identity match exactly what was requested.
	if err := verifySignedCommit(ctx, runner, input.WorkDir, signedSHA, validated.TreeSHA, parentSHA, message, signingCommitterName, signingCommitterEmail); err != nil {
		return SignedCommitResult{}, err
	}

	tags, err := recreateExpectedTags(ctx, runner, input.WorkDir, validated.ExpectedTags, signedSHA)
	if err != nil {
		return SignedCommitResult{}, err
	}

	fingerprint, publicKey, err := signAttestation(signer, signedAttestation{
		UnsignedSHA:     input.Output.CommitSHA,
		SourceSHA:       validated.SourceSHA,
		TreeSHA:         validated.TreeSHA,
		ReleaseSHA:      signedSHA,
		ResolvedVersion: validated.ResolvedVersion,
		Tags:            tagRefNames(tags),
	})
	if err != nil {
		return SignedCommitResult{}, err
	}

	return SignedCommitResult{
		SignedOutput: SignedOutput{
			UnsignedSHA:       input.Output.CommitSHA,
			SourceSHA:         validated.SourceSHA,
			TreeSHA:           validated.TreeSHA,
			ReleaseSHA:        signedSHA,
			CommitParentSHA:   parentSHA,
			SignerFingerprint: fingerprint,
			ChangedPaths:      changedPathsOf(input.Output),
			Tags:              tags,
		},
		SignerPublicKey: publicKey,
	}, nil
}

func changedPathsOf(output GenerationOutput) []string {
	paths := make([]string, 0, len(output.Changes))
	for _, change := range output.Changes {
		paths = append(paths, change.Path)
	}
	return paths
}

// verifySignedCommit reads back a constructed commit through `git
// cat-file -p` and confirms its tree, parent (or absence of one),
// message, and author/committer identity are exactly what was requested.
// A subprocess that exits zero is not proof by itself: `git commit-tree`
// could in principle be shadowed or misbehave, and §13.6 requires this
// job to run fixed executable paths but still verify their output, not
// merely trust their exit status.
func verifySignedCommit(ctx context.Context, runner CommandRunner, workDir, commitSHA, wantTree, wantParent, wantMessage, wantName, wantEmail string) error {
	result, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "cat-file", "-p", commitSHA},
		Env:  gitStateEnv(),
		Dir:  workDir,
	})
	if err != nil {
		return wrapReleaseError(ErrorClassGenerationFailed, "signing_verify_read_failed", err)
	}
	lines := strings.Split(result.Stdout, "\n")
	var gotTree string
	var gotParents []string
	var gotAuthor, gotCommitter string
	headerDone := false
	var messageLines []string
	for _, line := range lines {
		if headerDone {
			messageLines = append(messageLines, line)
			continue
		}
		if line == "" {
			headerDone = true
			continue
		}
		switch {
		case strings.HasPrefix(line, "tree "):
			gotTree = strings.TrimPrefix(line, "tree ")
		case strings.HasPrefix(line, "parent "):
			gotParents = append(gotParents, strings.TrimPrefix(line, "parent "))
		case strings.HasPrefix(line, "author "):
			gotAuthor = strings.TrimPrefix(line, "author ")
		case strings.HasPrefix(line, "committer "):
			gotCommitter = strings.TrimPrefix(line, "committer ")
		}
	}
	gotMessage := strings.TrimRight(strings.Join(messageLines, "\n"), "\n")
	if gotTree != wantTree {
		return newReleaseError(ErrorClassGenerationFailed, "signing_verify_tree_mismatch")
	}
	if wantParent == "" {
		if len(gotParents) != 0 {
			return newReleaseError(ErrorClassGenerationFailed, "signing_verify_parent_mismatch")
		}
	} else if len(gotParents) != 1 || gotParents[0] != wantParent {
		return newReleaseError(ErrorClassGenerationFailed, "signing_verify_parent_mismatch")
	}
	if gotMessage != wantMessage {
		return newReleaseError(ErrorClassGenerationFailed, "signing_verify_message_mismatch")
	}
	identityPrefix := wantName + " <" + wantEmail + ">"
	if !strings.HasPrefix(gotAuthor, identityPrefix) || !strings.HasPrefix(gotCommitter, identityPrefix) {
		return newReleaseError(ErrorClassGenerationFailed, "signing_verify_identity_mismatch")
	}
	return nil
}

// recreateExpectedTags creates one annotated tag per expectedTags entry,
// all pointing at signedSHA, then reads back each tag object's own SHA
// and its peeled commit SHA and rejects any tag that does not peel
// directly to signedSHA (no tag chains, matching §13.5: "Every release
// tag is annotated and peels directly to the recorded release commit.
// Reject lightweight tags, duplicate names, tag chains, unexpected tags,
// and tags outside the derived manifest.").
//
// The tagger itself already creates a local annotated tag per expected
// name at the *unsigned* commit during generation (createTagIfMissing in
// scripts/autoreleasetagger/main.go runs even with --disable-push, which
// only skips the push). Those tags point at the wrong commit for this
// step's purposes, so each is deleted here before being recreated at
// signedSHA; §13.5's "reject ... tag chains" is enforced by verifying
// the peeled commit below, not by trusting that a delete-then-recreate
// pair never raced with anything else in this single-writer checkout.
func recreateExpectedTags(ctx context.Context, runner CommandRunner, workDir string, expectedTags []string, signedSHA string) ([]TagRef, error) {
	seen := map[string]bool{}
	tags := make([]TagRef, 0, len(expectedTags))
	for _, name := range expectedTags {
		if seen[name] {
			return nil, newReleaseError(ErrorClassGenerationFailed, "duplicate_expected_tag")
		}
		seen[name] = true

		if _, err := runner.Run(ctx, Command{
			Path: "git",
			Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "tag", "--delete", name},
			Env:  gitStateEnv(),
			Dir:  workDir,
		}); err != nil {
			// A missing tag to delete is fine (not every caller's checkout ran
			// the tagger's own tag-creation step); only a genuine deletion
			// failure on an existing tag would be unexpected, and the
			// subsequent create call below will surface that as a distinct
			// error if the stale tag is still present.
			_ = err
		}

		env := append([]string(nil), gitStateEnv()...)
		env = append(env,
			"GIT_AUTHOR_NAME="+signingCommitterName, "GIT_AUTHOR_EMAIL="+signingCommitterEmail,
			"GIT_COMMITTER_NAME="+signingCommitterName, "GIT_COMMITTER_EMAIL="+signingCommitterEmail,
		)
		if _, err := runner.Run(ctx, Command{
			Path: "git",
			Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "-c", "tag.gpgsign=false", "tag", "-a", "-m", name, name, signedSHA},
			Env:  env,
			Dir:  workDir,
		}); err != nil {
			return nil, wrapReleaseError(ErrorClassGenerationFailed, "tag_creation_failed", err)
		}

		tagObjectResult, err := runner.Run(ctx, Command{
			Path: "git",
			Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "rev-parse", "refs/tags/" + name},
			Env:  gitStateEnv(),
			Dir:  workDir,
		})
		if err != nil {
			return nil, wrapReleaseError(ErrorClassGenerationFailed, "tag_read_failed", err)
		}
		tagObjectSHA := strings.TrimSpace(tagObjectResult.Stdout)

		peeledResult, err := runner.Run(ctx, Command{
			Path: "git",
			Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "rev-parse", "refs/tags/" + name + "^{commit}"},
			Env:  gitStateEnv(),
			Dir:  workDir,
		})
		if err != nil {
			return nil, wrapReleaseError(ErrorClassGenerationFailed, "tag_peel_failed", err)
		}
		peeledCommitSHA := strings.TrimSpace(peeledResult.Stdout)
		if peeledCommitSHA != signedSHA {
			return nil, newReleaseError(ErrorClassGenerationFailed, "tag_does_not_peel_to_signed_commit")
		}
		if !ValidGitObjectID(tagObjectSHA) || tagObjectSHA == peeledCommitSHA {
			// A lightweight tag's "tag object" would just be the commit
			// SHA itself (rev-parse on a lightweight tag returns the
			// commit, not a distinct tag object): reject that case
			// explicitly rather than silently accepting a non-annotated
			// tag, per §13.5's "Reject lightweight tags."
			return nil, newReleaseError(ErrorClassGenerationFailed, "tag_not_annotated")
		}

		tags = append(tags, TagRef{
			Name:            name,
			Ref:             "refs/tags/" + name,
			TagObjectSHA:    tagObjectSHA,
			PeeledCommitSHA: peeledCommitSHA,
		})
	}
	return tags, nil
}

func tagRefNames(tags []TagRef) []string {
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	return names
}

// signedAttestation is the canonical "what is being attested" payload
// signAttestation seals: a fixed, ordered set of fields rather than
// arbitrary free-form bytes, so verification can independently recompute
// the exact same bytes rather than trusting an opaque blob.
type signedAttestation struct {
	UnsignedSHA     string   `json:"unsigned_sha"`
	SourceSHA       string   `json:"source_sha"`
	TreeSHA         string   `json:"tree_sha"`
	ReleaseSHA      string   `json:"release_sha"`
	ResolvedVersion string   `json:"resolved_version"`
	Tags            []string `json:"tags"`
}

// signAttestation seals attestation with signer and returns the
// hex-encoded SHA-256 fingerprint of the raw public key bytes (§13.5's
// SignedOutput.SignerFingerprint) and the raw public key itself. The
// fingerprint is a fixed, independently-recomputable digest: given the
// same public key bytes, any verifier derives the identical fingerprint
// without needing the envelope that produced it.
func signAttestation(signer Signer, attestation signedAttestation) (fingerprint string, publicKey []byte, err error) {
	data, err := json.Marshal(attestation)
	if err != nil {
		return "", nil, wrapReleaseError(ErrorClassGenerationFailed, "attestation_marshal_failed", err)
	}
	envelope, err := Seal(signer, data)
	if err != nil {
		return "", nil, err
	}
	keyBytes, err := hex.DecodeString(envelope.PublicKey)
	if err != nil {
		return "", nil, newReleaseError(ErrorClassGenerationFailed, "attestation_key_malformed")
	}
	sum := sha256.Sum256(keyBytes)
	return hex.EncodeToString(sum[:]), keyBytes, nil
}

// VerifyAttestation independently recomputes and checks the signed
// attestation for a recorded SignedOutput against verifier/trustedPublicKey,
// re-deriving the exact canonical bytes from signed's own fields rather
// than trusting any stored envelope. It also confirms trustedPublicKey's
// fingerprint matches signed.SignerFingerprint, so a caller cannot verify
// against a key that was swapped after signing.
func VerifyAttestation(verifier Verifier, signed SignedOutput, envelope SignedEnvelope, trustedPublicKey []byte) error {
	sum := sha256.Sum256(trustedPublicKey)
	if hex.EncodeToString(sum[:]) != signed.SignerFingerprint {
		return newReleaseError(ErrorClassStateConflict, "signer_fingerprint_mismatch")
	}
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
		return wrapReleaseError(ErrorClassGenerationFailed, "attestation_marshal_failed", err)
	}
	if string(data) != string(envelope.Data) {
		return newReleaseError(ErrorClassStateConflict, "attestation_data_mismatch")
	}
	if _, err := Open(verifier, envelope, trustedPublicKey); err != nil {
		return err
	}
	return nil
}

// attestationResolvedVersion is a placeholder accessor kept distinct from
// a direct field read so VerifyAttestation's reconstruction logic has one
// place to adjust if SignedOutput ever needs to carry ResolvedVersion
// explicitly; today it is re-derived from the release tag names' shared
// suffix, matching validateExpectedTags's own invariant that every
// expected tag ends with the resolved version.
func attestationResolvedVersion(signed SignedOutput) string {
	for _, tag := range signed.Tags {
		if idx := strings.LastIndex(tag.Name, "/"); idx >= 0 {
			return tag.Name[idx+1:]
		}
		return tag.Name
	}
	return ""
}

// signedOutputDiff reports the first field that differs between two
// SignedOutput values, or "" if they match. Unlike Reservation's fields,
// SignedOutput is not part of the original immutable reservation (it is
// added later, at the `signed` phase transition), but once recorded it
// must be equally immutable for the life of the operation: "Do not
// regenerate timestamps, re-sign, or rebuild a recorded signed commit
// during resume."
func signedOutputDiff(existing, incoming *SignedOutput) string {
	if existing == nil {
		return ""
	}
	if incoming == nil {
		return "signed_output_removed"
	}
	switch {
	case existing.UnsignedSHA != incoming.UnsignedSHA:
		return "unsigned_sha"
	case existing.SourceSHA != incoming.SourceSHA:
		return "source_sha"
	case existing.TreeSHA != incoming.TreeSHA:
		return "tree_sha"
	case existing.ReleaseSHA != incoming.ReleaseSHA:
		return "release_sha"
	case existing.CommitParentSHA != incoming.CommitParentSHA:
		return "commit_parent_sha"
	case existing.SignerFingerprint != incoming.SignerFingerprint:
		return "signer_fingerprint"
	case !equalStringSlices(existing.ChangedPaths, incoming.ChangedPaths):
		return "changed_paths"
	case !equalTagRefs(existing.Tags, incoming.Tags):
		return "tags"
	case !equalBundle(existing.Bundle, incoming.Bundle):
		return "bundle"
	}
	return ""
}

func equalTagRefs(a, b []TagRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalBundle(a, b Bundle) bool {
	if a.Path != b.Path || a.SHA256 != b.SHA256 || a.SizeBytes != b.SizeBytes {
		return false
	}
	return equalStringSlices(a.PrerequisiteSHAs, b.PrerequisiteSHAs)
}

// AdvanceToSigned validates that existing (loaded via LoadState) is
// exactly at PhaseReserved (or already at PhaseSigned with byte-identical
// SignedOutput, for an idempotent retry) and returns the Record to
// persist for the `signed` transition: never recomputing a resolved
// version, never re-signing, and never invoking SignCommit again for an
// already-signed operation.
//
// Callers must check IsAlreadySigned on the returned decision before
// calling SignCommit: when existing is already PhaseSigned with matching
// SignedOutput, AdvanceToSigned returns the existing record unchanged and
// the caller must not construct a new signed commit at all.
func AdvanceToSigned(existing Record, signed SignedOutput, evidence json.RawMessage) (Record, bool, error) {
	if existing.Phase == PhaseSigned {
		if diff := signedOutputDiff(existing.SignedOutput, &signed); diff != "" {
			return Record{}, false, newReleaseError(ErrorClassStateConflict, "signed_output_changed")
		}
		// Idempotent retry: the state push already succeeded earlier: return
		// the existing record unchanged. isAlreadySigned=true tells the
		// caller not to sign or persist again.
		return existing, true, nil
	}
	nextPhase, err := AdvancePhase(existing.Phase, PhaseSigned)
	if err != nil {
		return Record{}, false, err
	}
	events, err := AppendEvent(existing.Events, existing.Reservation.RequestKey, EventPhaseAdvanced, evidence)
	if err != nil {
		return Record{}, false, err
	}
	record := existing
	record.Phase = nextPhase
	record.SignedOutput = &signed
	record.Events = events
	return record, false, nil
}
