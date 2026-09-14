// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed by Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gardenerrelease

import (
	"context"
	"path/filepath"
	"testing"
)

func TestUnsignedGenerationBundleRestoresExactCommit(t *testing.T) {
	remote, output, _, workDir := bundleFixture(t, "dev-v2.9.x", "v2.9.0-dev")
	bundlePath := filepath.Join(t.TempDir(), UnsignedRepositoryBundleName)
	digest, err := BuildUnsignedRepositoryBundle(context.Background(), ExecRunner{}, workDir, bundlePath, output)
	if err != nil {
		t.Fatal(err)
	}
	restored := t.TempDir()
	runGit(t, restored, "init", "--quiet", "-b", "restore")
	runGit(t, restored, "-c", "protocol.file.allow=always", "fetch", "--quiet", "--no-tags", remote, output.SourceSHA)
	if err := RestoreUnsignedRepositoryBundle(context.Background(), ExecRunner{}, restored, bundlePath, digest, output); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, restored, "rev-parse", "refs/heads/gardener-release-unsigned-artifact"); got != output.CommitSHA+"\n" {
		t.Fatalf("commit = %q, want %s", got, output.CommitSHA)
	}
}
