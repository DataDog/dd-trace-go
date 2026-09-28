// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fuzzexample

import (
	"fmt"
	"os"
	"testing"
)

func NativeParity()    {}
func NativeUnordered() {}
func NativeMismatch()  {}
func NativePanic()     {}
func WithoutOutput()   {}
func PanicNil()        {}

func TestNormalSelection(t *testing.T) {
	t.Log("selected normal test")
}

func FuzzNativeParity(f *testing.F) {
	f.Add("alpha")
	f.Add("beta")
	f.Fuzz(func(t *testing.T, value string) {
		t.Logf("seed value: %s", value)
		if value == "beta" && os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") == "fuzz-failure" {
			t.Errorf("unexpected seed value: %s", value)
		}
	})
}

func FuzzNativeSkip(f *testing.F) {
	f.Add("skip")
	f.Fuzz(func(t *testing.T, value string) {
		if value == "skip" {
			t.Skip("intentional seed skip")
		}
	})
}

func FuzzNativeTypes(f *testing.F) {
	f.Add(42, []byte("payload"), true, 1.5)
	f.Fuzz(func(t *testing.T, number int, data []byte, enabled bool, ratio float64) {
		if number != 42 || string(data) != "payload" || !enabled || ratio != 1.5 {
			t.Fatal("fuzz callback arguments changed")
		}
	})
}

func FuzzSeedCleanupFailure(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "seed-lifecycle" {
		f.Skip("seed cleanup regression is not selected")
	}
	f.Add("cleanup-failure")
	f.Fuzz(func(t *testing.T, _ string) {
		t.Cleanup(func() { t.Error("seed cleanup failure") })
	})
}

func FuzzSeedCleanupSkip(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "seed-lifecycle" {
		f.Skip("seed cleanup regression is not selected")
	}
	f.Add("cleanup-skip")
	f.Fuzz(func(t *testing.T, _ string) {
		t.Cleanup(func() { t.Skip("seed cleanup skip") })
	})
}

func FuzzSeedParallelFailure(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "seed-lifecycle" {
		f.Skip("parallel seed regression is not selected")
	}
	f.Add("parallel-failure")
	f.Fuzz(func(t *testing.T, _ string) {
		t.Run("parallel", func(t *testing.T) {
			t.Parallel()
			t.Error("parallel child failure")
		})
	})
}

func FuzzMissingCall(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "fuzz-missing-call" {
		f.Skip("missing F.Fuzz regression is not selected")
	}
}

func FuzzActiveOther(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "active-fuzz" {
		f.Skip("active fuzz regression is not selected")
	}
	f.Add("other-seed")
	f.Fuzz(func(t *testing.T, _ string) {
		t.Log("ordinary seed for the non-selected fuzz target")
	})
}

func ExampleNativeParity() {
	fmt.Println("native example output")
	// Output: native example output
}

func ExampleNativeUnordered() {
	fmt.Println("second")
	fmt.Println("first")
	// Unordered output:
	// first
	// second
}

func ExampleNativeMismatch() {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") == "example-mismatch" {
		fmt.Println("actual")
	} else {
		fmt.Println("expected")
	}
	// Output: expected
}

func ExampleNativePanic() {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") == "example-panic" {
		panic("example panic sentinel")
	}
	fmt.Println("safe")
	// Output: safe
}

func ExamplePanicNil() {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") == "example-panic-nil" {
		panic(nil)
	}
	fmt.Println("safe")
	// Output: safe
}

func ExampleWithoutOutput() {
	panic("an example without an output directive must not run")
}
