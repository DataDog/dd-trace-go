// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

const UnsignedRepositoryBundleName = "gardener-release-unsigned-v1.bundle"

// BuildUnsignedRepositoryBundle creates a thin, fixed-ref transport bundle.
func BuildUnsignedRepositoryBundle(ctx context.Context, runner CommandRunner, workDir, outputPath string, output GenerationOutput) (string, error) {
	if filepath.Base(outputPath) != UnsignedRepositoryBundleName || !ValidGitObjectID(output.SourceSHA) || !ValidGitObjectID(output.CommitSHA) {
		return "", newReleaseError(ErrorClassContractMismatch, "invalid_generation_bundle")
	}
	ref := "refs/heads/gardener-release-unsigned-artifact"
	if _, err := runner.Run(ctx, Command{Path: "git", Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "branch", "--force", "gardener-release-unsigned-artifact", output.CommitSHA}, Env: gitStateEnv(), Dir: workDir}); err != nil {
		return "", wrapReleaseError(ErrorClassGenerationFailed, "generation_bundle_prepare_failed", err)
	}
	if _, err := runner.Run(ctx, Command{Path: "git", Args: []string{"-c", "protocol.file.allow=never", "-c", "core.hooksPath=/dev/null", "bundle", "create", outputPath, ref, "^" + output.SourceSHA}, Env: gitStateEnv(), Dir: workDir}); err != nil {
		return "", wrapReleaseError(ErrorClassGenerationFailed, "generation_bundle_create_failed", err)
	}
	if _, err := verifyBundleContents(ctx, runner, workDir, outputPath, map[string]string{ref: output.CommitSHA}, []string{output.SourceSHA}); err != nil {
		return "", err
	}
	data, err := os.ReadFile(outputPath)
	if err != nil || len(data) == 0 || len(data) > MaxRecoveryBundleBytes {
		return "", newReleaseError(ErrorClassGenerationFailed, "generation_bundle_invalid")
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// RestoreUnsignedRepositoryBundle verifies and imports a generation bundle
// after the protected job fetches the recorded source prerequisite.
func RestoreUnsignedRepositoryBundle(ctx context.Context, runner CommandRunner, workDir, bundlePath, expectedDigest string, output GenerationOutput) error {
	if filepath.Base(bundlePath) != UnsignedRepositoryBundleName || !lowerHexDigest(expectedDigest) {
		return newReleaseError(ErrorClassContractMismatch, "invalid_generation_bundle")
	}
	data, err := os.ReadFile(bundlePath)
	if err != nil || len(data) == 0 || len(data) > MaxRecoveryBundleBytes {
		return newReleaseError(ErrorClassEvidenceIncomplete, "generation_bundle_missing")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != expectedDigest {
		return newReleaseError(ErrorClassStateConflict, "generation_bundle_digest_mismatch")
	}
	ref := "refs/heads/gardener-release-unsigned-artifact"
	if _, err := verifyBundleContents(ctx, runner, workDir, bundlePath, map[string]string{ref: output.CommitSHA}, []string{output.SourceSHA}); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, Command{Path: "git", Args: []string{"-c", "protocol.file.allow=always", "-c", "core.hooksPath=/dev/null", "fetch", "--no-tags", bundlePath, ref + ":" + ref}, Env: gitStateEnv(), Dir: workDir}); err != nil {
		return wrapReleaseError(ErrorClassGenerationFailed, "generation_bundle_import_failed", err)
	}
	return nil
}
