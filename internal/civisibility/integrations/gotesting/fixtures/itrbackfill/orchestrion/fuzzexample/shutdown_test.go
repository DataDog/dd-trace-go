//go:build fuzzexamplefixture

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fuzzexample

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/internal/fuzzfixture"
)

func FuzzFatalRoot(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "fatal-child" {
		f.Skip("fatal lifecycle fixture requires its subprocess harness")
	}
	panic("root panic sentinel")
}

func FuzzFatalSeed(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "fatal-child" {
		f.Skip("fatal lifecycle fixture requires its subprocess harness")
	}
	f.Add(0)
	f.Add(1)
	f.Fuzz(func(t *testing.T, seed int) {
		if seed == 0 {
			panic("seed panic sentinel")
		}
		fmt.Println("NEXT_SEED_EXECUTED")
	})
}

func FuzzFatalSeedCleanup(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "fatal-child" {
		f.Skip("fatal lifecycle fixture requires its subprocess harness")
	}
	f.Add(0)
	f.Add(1)
	f.Fuzz(func(t *testing.T, seed int) {
		if seed == 0 {
			t.Cleanup(func() { panic("seed cleanup panic sentinel") })
			return
		}
		fmt.Println("NEXT_SEED_EXECUTED")
	})
}

func FuzzFatalSeedBodyCleanup(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "fatal-child" {
		f.Skip("fatal lifecycle fixture requires its subprocess harness")
	}
	f.Add(0)
	f.Add(1)
	f.Fuzz(func(t *testing.T, seed int) {
		if seed == 0 {
			t.Cleanup(func() { panic("seed cleanup panic sentinel") })
			panic("seed body panic sentinel")
		}
		fmt.Println("NEXT_SEED_EXECUTED")
	})
}

func TestFuzzFatalShutdown(t *testing.T) {
	if os.Getenv("DD_FUZZ_EXAMPLE_MODE") == "" {
		t.Skip("fixture requires the fuzz/example harness")
	}
	targets := map[string]func(*testing.F){
		"root":              FuzzFatalRoot,
		"seed":              FuzzFatalSeed,
		"seed-cleanup":      FuzzFatalSeedCleanup,
		"seed-body-cleanup": FuzzFatalSeedBodyCleanup,
		"root-body-error":   FuzzFatalRootBodyCleanup,
		"root-body-fatal":   FuzzFatalRootBodyCleanup,
		"seed-body-error":   FuzzFatalSeedBodyError,
		"seed-body-fatal":   FuzzFatalSeedBodyError,
	}
	for _, scenario := range fuzzfixture.FatalScenarios() {
		t.Run(scenario, func(t *testing.T) {
			fuzzfixture.CheckFatalShutdown(t, scenario, targets[strings.TrimPrefix(scenario, "managed-")])
		})
	}
}

func FuzzFatalRootBodyCleanup(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "fatal-child" {
		f.Skip("fatal lifecycle fixture requires its subprocess harness")
	}
	f.Cleanup(func() {
		if strings.HasSuffix(os.Getenv("DD_FUZZ_FATAL_KIND"), "-fatal") {
			f.Fatal("root cleanup Fatal sentinel")
		}
		f.Error("root cleanup Error sentinel")
	})
	panic("root body panic sentinel")
}

func FuzzFatalSeedBodyError(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "fatal-child" {
		f.Skip("fatal lifecycle fixture requires its subprocess harness")
	}
	f.Add(0)
	f.Add(1)
	f.Fuzz(func(t *testing.T, seed int) {
		if seed == 0 {
			t.Cleanup(func() {
				if strings.HasSuffix(os.Getenv("DD_FUZZ_FATAL_KIND"), "-fatal") {
					t.Fatal("seed cleanup Fatal sentinel")
				}
				t.Error("seed cleanup Error sentinel")
			})
			panic("seed body panic sentinel")
		}
		fmt.Println("NEXT_SEED_EXECUTED")
	})
}
