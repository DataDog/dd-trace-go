// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sort"
	"strings"
)

// BuildRecoveryBundleInput is everything BuildRecoveryBundle needs to
// create a thin recovery bundle covering exactly the new objects a signed
// commit and its tags introduced.
type BuildRecoveryBundleInput struct {
	// WorkDir is the Git repository containing SourceSHA, ReleaseSHA, and
	// every tag in Tags (the same checkout SignCommit produced them in).
	WorkDir    string
	SourceSHA  string
	ReleaseSHA string
	Tags       []TagRef
	// OutputPath is where the bundle file is written, inside WorkDir or
	// another caller-owned directory. BuildRecoveryBundle never writes
	// outside this exact path.
	OutputPath string
}

// BuildRecoveryBundle creates a thin bundle (SourceSHA as prerequisite,
// ReleaseSHA and every tag ref as the bundled tips), independently
// verifies its contents before returning success, and enforces
// §13.2's "Recovery bundle: At most 50 MiB" cap. It never trusts `git
// bundle create`'s exit code alone: see verifyBundleContents.
func BuildRecoveryBundle(ctx context.Context, runner CommandRunner, input BuildRecoveryBundleInput) (Bundle, error) {
	if !ValidGitObjectID(input.SourceSHA) || !ValidGitObjectID(input.ReleaseSHA) {
		return Bundle{}, newReleaseError(ErrorClassContractMismatch, "invalid_git_object")
	}
	if len(input.Tags) == 0 {
		return Bundle{}, newReleaseError(ErrorClassGenerationFailed, "empty_expected_tags")
	}

	expectedRefs := map[string]string{"refs/heads/gardener-release-bundle-tip": input.ReleaseSHA}
	args := []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "bundle", "create", input.OutputPath}
	// A bundle needs at least one ref argument; a detached ReleaseSHA has
	// no ref of its own, so a throwaway local branch name is created to
	// name it. This branch never leaves WorkDir and is not part of the
	// bundle's own recorded ref set beyond naming the release commit tip.
	if _, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "branch", "--force", "gardener-release-bundle-tip", input.ReleaseSHA},
		Env:  gitStateEnv(),
		Dir:  input.WorkDir,
	}); err != nil {
		return Bundle{}, wrapReleaseError(ErrorClassGenerationFailed, "bundle_prepare_failed", err)
	}
	args = append(args, "refs/heads/gardener-release-bundle-tip")
	for _, tag := range input.Tags {
		args = append(args, tag.Ref)
		expectedRefs[tag.Ref] = tag.TagObjectSHA
	}
	args = append(args, "^"+input.SourceSHA)

	if _, err := runner.Run(ctx, Command{
		Path: "git",
		Args: args,
		Env:  gitStateEnv(),
		Dir:  input.WorkDir,
	}); err != nil {
		return Bundle{}, wrapReleaseError(ErrorClassGenerationFailed, "bundle_create_failed", err)
	}

	data, err := os.ReadFile(input.OutputPath)
	if err != nil {
		return Bundle{}, wrapReleaseError(ErrorClassEvidenceIncomplete, "bundle_read_failed", err)
	}
	if err := checkRecoveryBundleSizeCap(len(data)); err != nil {
		return Bundle{}, err
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])

	// Do not trust `git bundle create`'s exit code alone: independently
	// list the bundle's refs and prerequisites and compare against the
	// exact expected set before ever treating it as "the bundle."
	prerequisites, err := verifyBundleContents(ctx, runner, input.WorkDir, input.OutputPath, expectedRefs, []string{input.SourceSHA})
	if err != nil {
		return Bundle{}, err
	}

	return Bundle{
		Path:             input.OutputPath,
		SHA256:           digest,
		SizeBytes:        int64(len(data)),
		PrerequisiteSHAs: prerequisites,
	}, nil
}

// checkRecoveryBundleSizeCap enforces §13.2's "Recovery bundle: At most
// 50 MiB. Stop before publishing if the bundle exceeds the cap," as a
// standalone, directly testable check independent of `git bundle
// create`'s own output size (which this small test fixture can never
// practically inflate past 50 MiB).
func checkRecoveryBundleSizeCap(sizeBytes int) error {
	if sizeBytes > MaxRecoveryBundleBytes {
		return newReleaseError(ErrorClassGenerationFailed, "bundle_exceeds_size_cap")
	}
	return nil
}

// verifyBundleContents independently re-derives a bundle's ref set and
// prerequisite commit SHAs using `git bundle list-heads` and `git bundle
// verify`'s own stated prerequisites, and rejects the bundle unless its
// refs are exactly expectedRefs (same names, same target SHAs, no
// extras) and its prerequisites are exactly wantPrerequisites. This is
// deliberately independent of trusting `git bundle verify`'s bare exit
// code: "Do not trust a bundle merely because `git bundle verify`
// succeeds. Compare digest, refs, prerequisites, and manifest too."
func verifyBundleContents(ctx context.Context, runner CommandRunner, workDir, bundlePath string, expectedRefs map[string]string, wantPrerequisites []string) ([]string, error) {
	headsResult, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "bundle", "list-heads", bundlePath},
		Env:  gitStateEnv(),
		Dir:  workDir,
	})
	if err != nil {
		return nil, wrapReleaseError(ErrorClassGenerationFailed, "bundle_list_heads_failed", err)
	}
	gotRefs := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(headsResult.Stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, newReleaseError(ErrorClassGenerationFailed, "bundle_heads_unparseable")
		}
		gotRefs[fields[1]] = fields[0]
	}
	if len(gotRefs) != len(expectedRefs) {
		return nil, newReleaseError(ErrorClassGenerationFailed, "bundle_unexpected_refs")
	}
	for ref, wantSHA := range expectedRefs {
		gotSHA, ok := gotRefs[ref]
		if !ok || gotSHA != wantSHA {
			return nil, newReleaseError(ErrorClassGenerationFailed, "bundle_unexpected_refs")
		}
	}

	// No --quiet here: git bundle verify's non-quiet stdout is the only
	// source for parseBundlePrerequisites below ("The bundle requires
	// this ref: <sha> "); --quiet suppresses that listing entirely.
	verifyResult, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "bundle", "verify", bundlePath},
		Env:  gitStateEnv(),
		Dir:  workDir,
	})
	if err != nil {
		// `git bundle verify` fails when a prerequisite is missing from
		// the checking repository. Since workDir already has SourceSHA
		// (the checkout Generate/SignCommit produced it in), a failure
		// here means the bundle itself is malformed, truncated, or
		// otherwise incomplete: reject as generation_failed rather than
		// evidence_incomplete, matching S05's "no release branch push."
		return nil, wrapReleaseError(ErrorClassGenerationFailed, "bundle_verify_failed", err)
	}
	gotPrerequisites := parseBundlePrerequisites(verifyResult.Stdout)
	sort.Strings(gotPrerequisites)
	wantSorted := append([]string(nil), wantPrerequisites...)
	sort.Strings(wantSorted)
	if !equalStringSlices(gotPrerequisites, wantSorted) {
		return nil, newReleaseError(ErrorClassGenerationFailed, "bundle_unexpected_prerequisites")
	}
	return gotPrerequisites, nil
}

// parseBundlePrerequisites extracts prerequisite commit SHAs from `git
// bundle verify`'s human-readable stdout. Its output has two SHA-listing
// sections: "The bundle contains ... ref(s):" (the bundle's own tips,
// not prerequisites) followed by "The bundle requires ... ref(s):" (the
// actual prerequisites this function must return), each followed by one
// "<sha> <subject-or-empty>" line per ref. Only SHAs after the
// "requires" marker are collected, so a bundle's own contained refs are
// never miscounted as prerequisites. This is a text scan over a
// known-stable, locally-produced Git command's own output (not untrusted
// network input), used only as one of several independent checks
// verifyBundleContents performs.
func parseBundlePrerequisites(stdout string) []string {
	var shas []string
	inRequires := false
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "The bundle requires") {
			inRequires = true
			continue
		}
		if strings.HasPrefix(line, "The bundle contains") || strings.HasPrefix(line, "The bundle uses") {
			inRequires = false
			continue
		}
		if !inRequires {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if ValidGitObjectID(fields[0]) {
			shas = append(shas, fields[0])
		}
	}
	return shas
}

// RestoreFromBundleInput is everything RestoreFromBundle needs to import
// a recovery bundle into a brand-new, empty clone and confirm it
// restores the exact recorded release SHA.
type RestoreFromBundleInput struct {
	// FreshWorkDir is an empty directory the caller owns exclusively; a
	// fresh Git repository is created here. It must never be the
	// directory that originally produced the bundle: recovery must prove
	// the bundle alone is sufficient, so callers discard the generating
	// workdir before calling this.
	FreshWorkDir string
	// SourceRemotePath is a local path to a remote that still has
	// SourceSHA reachable from an approved protected ref (the fixture
	// "source" remote in tests, standing in for dd-trace-go's real main
	// branch in production). RestoreFromBundle fetches the prerequisite
	// from here, never from the bundle itself and never by regenerating
	// or substituting a different source commit.
	SourceRemotePath   string
	SourceSHA          string
	BundlePath         string
	ExpectedBundle     Bundle
	ExpectedReleaseSHA string
	ExpectedTreeSHA    string
	ExpectedTags       []TagRef
}

// RestoreFromBundle imports ExpectedBundle into a brand-new clone at
// FreshWorkDir (after first fetching SourceSHA as the bundle's
// prerequisite from SourceRemotePath) and asserts every recovered
// commit/tree/tag-object/peeled-commit SHA is byte-identical to what was
// recorded. It never regenerates or re-signs anything: recovery either
// reproduces the exact recorded objects from the bundle, or it fails.
func RestoreFromBundle(ctx context.Context, runner CommandRunner, input RestoreFromBundleInput) error {
	if !ValidGitObjectID(input.SourceSHA) || !ValidGitObjectID(input.ExpectedReleaseSHA) {
		return newReleaseError(ErrorClassContractMismatch, "invalid_git_object")
	}
	data, err := os.ReadFile(input.BundlePath)
	if err != nil {
		return wrapReleaseError(ErrorClassEvidenceIncomplete, "bundle_read_failed", err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != input.ExpectedBundle.SHA256 {
		return newReleaseError(ErrorClassStateConflict, "bundle_digest_mismatch")
	}
	if int64(len(data)) != input.ExpectedBundle.SizeBytes {
		return newReleaseError(ErrorClassStateConflict, "bundle_size_mismatch")
	}

	if _, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"init", "--quiet", "-b", "recovery"},
		Env:  gitStateEnv(),
		Dir:  input.FreshWorkDir,
	}); err != nil {
		return wrapReleaseError(ErrorClassGenerationFailed, "recovery_init_failed", err)
	}

	// Fetch the prerequisite from the still-available source remote,
	// never from the (by now discarded) generating workdir and never by
	// regenerating the commit: "If prerequisites are unavailable, stop.
	// Never regenerate the commit or substitute a newer source."
	if _, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=always", "-c", "core.hooksPath=/dev/null", "fetch", "--quiet", input.SourceRemotePath, input.SourceSHA},
		Env:  gitStateEnv(),
		Dir:  input.FreshWorkDir,
	}); err != nil {
		return wrapReleaseError(ErrorClassEvidenceIncomplete, "recovery_prerequisite_fetch_failed", err)
	}
	fetchedSourceResult, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "cat-file", "-e", input.SourceSHA},
		Env:  gitStateEnv(),
		Dir:  input.FreshWorkDir,
	})
	_ = fetchedSourceResult
	if err != nil {
		return newReleaseError(ErrorClassEvidenceIncomplete, "recovery_prerequisite_unreachable")
	}

	// Independently re-verify the bundle's own contents from this fresh
	// clone before importing it, exactly as BuildRecoveryBundle did at
	// creation time: a bundle that passed verification once could still
	// have been swapped for a different (structurally valid) bundle
	// before recovery, so this re-derives the check rather than trusting
	// the caller's ExpectedBundle metadata alone.
	expectedRefs := map[string]string{"refs/heads/gardener-release-bundle-tip": input.ExpectedReleaseSHA}
	for _, tag := range input.ExpectedTags {
		expectedRefs[tag.Ref] = tag.TagObjectSHA
	}
	if _, err := verifyBundleContents(ctx, runner, input.FreshWorkDir, input.BundlePath, expectedRefs, []string{input.SourceSHA}); err != nil {
		return err
	}

	// `git bundle unbundle` only unpacks objects into the object database;
	// it does not create any ref. Fetch every bundled ref explicitly (each
	// as <ref>:<same-ref>) so the recovered commit and every tag object
	// become reachable through named refs, matching what a real resume
	// path needs to publish from.
	fetchRefs := []string{"refs/heads/gardener-release-bundle-tip:refs/heads/gardener-release-bundle-tip"}
	for _, tag := range input.ExpectedTags {
		fetchRefs = append(fetchRefs, tag.Ref+":"+tag.Ref)
	}
	fetchArgs := append([]string{"-c", "protocol.file.allow=always", "-c", "core.hooksPath=/dev/null", "fetch", "--no-tags", input.BundlePath}, fetchRefs...)
	if _, err := runner.Run(ctx, Command{
		Path: "git",
		Args: fetchArgs,
		Env:  gitStateEnv(),
		Dir:  input.FreshWorkDir,
	}); err != nil {
		return wrapReleaseError(ErrorClassGenerationFailed, "bundle_unbundle_failed", err)
	}

	if err := verifyRecoveredCommit(ctx, runner, input.FreshWorkDir, input.ExpectedReleaseSHA, input.ExpectedTreeSHA); err != nil {
		return err
	}
	for _, tag := range input.ExpectedTags {
		if err := verifyRecoveredTag(ctx, runner, input.FreshWorkDir, tag); err != nil {
			return err
		}
	}
	return nil
}

func verifyRecoveredCommit(ctx context.Context, runner CommandRunner, workDir, wantCommitSHA, wantTreeSHA string) error {
	result, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "cat-file", "-e", wantCommitSHA},
		Env:  gitStateEnv(),
		Dir:  workDir,
	})
	_ = result
	if err != nil {
		return newReleaseError(ErrorClassStateConflict, "recovery_commit_missing")
	}
	treeResult, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "rev-parse", wantCommitSHA + "^{tree}"},
		Env:  gitStateEnv(),
		Dir:  workDir,
	})
	if err != nil {
		return wrapReleaseError(ErrorClassGenerationFailed, "recovery_tree_read_failed", err)
	}
	if strings.TrimSpace(treeResult.Stdout) != wantTreeSHA {
		return newReleaseError(ErrorClassStateConflict, "recovery_tree_mismatch")
	}
	return nil
}

func verifyRecoveredTag(ctx context.Context, runner CommandRunner, workDir string, tag TagRef) error {
	objectResult, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "rev-parse", tag.Ref},
		Env:  gitStateEnv(),
		Dir:  workDir,
	})
	if err != nil {
		return wrapReleaseError(ErrorClassStateConflict, "recovery_tag_missing", err)
	}
	if strings.TrimSpace(objectResult.Stdout) != tag.TagObjectSHA {
		return newReleaseError(ErrorClassStateConflict, "recovery_tag_object_mismatch")
	}
	peeledResult, err := runner.Run(ctx, Command{
		Path: "git",
		Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "rev-parse", tag.Ref + "^{commit}"},
		Env:  gitStateEnv(),
		Dir:  workDir,
	})
	if err != nil {
		return wrapReleaseError(ErrorClassGenerationFailed, "recovery_tag_peel_failed", err)
	}
	if strings.TrimSpace(peeledResult.Stdout) != tag.PeeledCommitSHA {
		return newReleaseError(ErrorClassStateConflict, "recovery_tag_peeled_mismatch")
	}
	return nil
}
