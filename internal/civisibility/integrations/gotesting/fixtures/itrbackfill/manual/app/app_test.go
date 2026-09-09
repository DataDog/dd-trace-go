// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/coverage"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/manual/lib"
)

func TestCoversLib(t *testing.T) {
	if lib.Answer() != 42 {
		t.Fatal("unexpected answer")
	}
}

func TestRunsNormally(t *testing.T) {
	t.Log("normal test")
}

func TestAddsIncompatibleProcessCoverage(t *testing.T) {
	if os.Getenv("DD_ITR_BACKFILL_SCENARIO") != "manual-process-coverage-merge-failure" {
		t.Skip("fixture scenario only")
	}
	profile := filepath.Join(t.TempDir(), "isolated.out")
	if err := os.WriteFile(profile, []byte("mode: set\nfixture/file.go:1.1,1.2 1 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := coverage.MergeProcessCoverageProfile(profile); err != nil {
		t.Fatal(err)
	}
}
