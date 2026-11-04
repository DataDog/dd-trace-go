// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting/fixtures/itrbackfill/internal/fuzzfixture"
)

func FuzzRootSkipLifecycle(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "lifecycle-child" {
		f.Skip("skip lifecycle fixture requires its subprocess harness")
	}
	f.Cleanup(func() {
		fmt.Println("SKIP_CLEANUP_EXECUTED")
		switch os.Getenv("DD_FUZZ_SKIP_CLEANUP") {
		case "error":
			f.Error("cleanup Error sentinel")
		case "fatal":
			f.Fatal("cleanup Fatal sentinel")
		case "panic":
			panic("cleanup panic sentinel")
		case "skipnow":
			f.SkipNow()
		case "goexit":
			runtime.Goexit()
		}
	})
	switch os.Getenv("DD_FUZZ_SKIP_METHOD") {
	case "skip":
		f.Skip("body skip sentinel")
	case "skipf":
		f.Skipf("body %s sentinel", "skip")
	case "skipnow":
		f.SkipNow()
	case "panic":
		panic("body panic sentinel")
	}
}

func FuzzSeedSkipLifecycle(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "lifecycle-child" {
		f.Skip("skip lifecycle fixture requires its subprocess harness")
	}
	f.Add(0)
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, _ int) {
		t.Cleanup(func() {
			fmt.Println("SKIP_CLEANUP_EXECUTED")
			switch os.Getenv("DD_FUZZ_SKIP_CLEANUP") {
			case "error":
				t.Error("cleanup Error sentinel")
			case "fatal":
				t.Fatal("cleanup Fatal sentinel")
			case "panic":
				panic("cleanup panic sentinel")
			case "skipnow":
				t.SkipNow()
			case "goexit":
				runtime.Goexit()
			}
		})
		switch os.Getenv("DD_FUZZ_SKIP_METHOD") {
		case "skip":
			t.Skip("body skip sentinel")
		case "skipf":
			t.Skipf("body %s sentinel", "skip")
		case "skipnow":
			t.SkipNow()
		case "panic":
			panic("body panic sentinel")
		}
	})
}

func FuzzParallelDuration(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "lifecycle-child" {
		f.Skip("timing fixture requires its subprocess harness")
	}
	f.Add(0)
	f.Add(1)
	var completed atomic.Int32
	f.Cleanup(func() {
		if got := completed.Load(); got != 2 {
			f.Errorf("root cleanup ran before both parallel seeds finished: %d", got)
		}
		fmt.Println("PARALLEL_ROOT_CLEANUP_EXECUTED")
	})
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, _ int) {
		t.Parallel()
		t.Cleanup(func() { completed.Add(1) })
	})
}

func TestFuzzSkipLifecycle(t *testing.T) {
	if os.Getenv("DD_FUZZ_EXAMPLE_MODE") == "" {
		t.Skip("fixture requires the fuzz/example harness")
	}
	for _, scenario := range fuzzfixture.SkipScenarios() {
		t.Run(scenario, func(t *testing.T) {
			target := FuzzRootSkipLifecycle
			if strings.HasPrefix(scenario, "seed-") {
				target = FuzzSeedSkipLifecycle
			}
			fuzzfixture.CheckSkipLifecycle(t, scenario, os.Getenv("DD_FUZZ_EXAMPLE_MODE"), target)
		})
	}
}

func TestFuzzParallelDuration(t *testing.T) {
	if os.Getenv("DD_FUZZ_EXAMPLE_MODE") == "" {
		t.Skip("fixture requires the fuzz/example harness")
	}
	fuzzfixture.CheckParallelDuration(t, FuzzParallelDuration)
}
