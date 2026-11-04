//go:build fuzzexamplefixture

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fuzzexample

import (
	"os"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/internal/fuzzfixture"
)

func TestMain(m *testing.M) {
	if os.Getenv("DD_FUZZ_EXAMPLE_MODE") == "" {
		os.Exit(m.Run())
	}
	run := func() int { return m.Run() }
	if fuzzfixture.IsFuzzWorker() {
		os.Exit(run())
	}
	if !built.WithOrchestrion {
		panic("expected fixture to run with Orchestrion")
	}
	switch os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") {
	case "corpus-child":
		exitCode := run()
		reportCorpusMemory()
		os.Exit(exitCode)
	case "fatal-child", "lifecycle-child":
		os.Exit(run())
	}
	os.Exit(fuzzfixture.Run(os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO"), os.Getenv("DD_FUZZ_EXAMPLE_MODE"), run, FuzzManagedSeeds, &nativeParityRuns, managedCombinedSeedRuns[:]))
}
