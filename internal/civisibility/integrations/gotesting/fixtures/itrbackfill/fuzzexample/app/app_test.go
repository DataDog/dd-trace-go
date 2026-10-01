// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package app

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting"
)

func NativeParity()             {}
func NativeUnordered()          {}
func NativeMismatch()           {}
func NativePanic()              {}
func WithoutOutput()            {}
func PanicNil()                 {}
func ManagedDisabled()          {}
func ManagedQuarantined()       {}
func ManagedQuarantinedPanic()  {}
func ManagedQuarantinedGoexit() {}
func ManagedAttemptToFix()      {}

func TestNormalSelection(t *testing.T) {
	t.Log("selected normal test")
}

var nativeParityRuns atomic.Int32

func FuzzNativeParity(f *testing.F) {
	nativeParityRuns.Add(1)
	f.Add("alpha")
	f.Add("beta")
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, value string) {
		t.Logf("seed value: %s", value)
		if value == "beta" && os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") == "fuzz-failure" {
			t.Errorf("unexpected seed value: %s", value)
		}
	})
}

func FuzzNativeSkip(f *testing.F) {
	f.Add("skip")
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, value string) {
		if value == "skip" {
			t.Skip("intentional seed skip")
		}
	})
}

func FuzzNativeTypes(f *testing.F) {
	f.Add(42, []byte("payload"), true, 1.5)
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, number int, data []byte, enabled bool, ratio float64) {
		if number != 42 || string(data) != "payload" || !enabled || ratio != 1.5 {
			t.Fatal("fuzz callback arguments changed")
		}
	})
}

func FuzzRootCleanupGoexit(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "root-cleanup-goexit" {
		f.Skip("root cleanup Goexit regression is not selected")
	}
	f.Cleanup(runtime.Goexit)
	f.Add(0)
	gotesting.GetFuzz(f).Fuzz(func(*testing.T, int) {})
}

func FuzzSeedCleanupFailure(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "seed-lifecycle" {
		f.Skip("seed cleanup regression is not selected")
	}
	f.Add("cleanup-failure")
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, _ string) {
		t.Cleanup(func() { t.Error("seed cleanup failure") })
	})
}

func FuzzSeedCleanupSkip(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "seed-lifecycle" {
		f.Skip("seed cleanup regression is not selected")
	}
	f.Add("cleanup-skip")
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, _ string) {
		t.Cleanup(func() { t.Skip("seed cleanup skip") })
	})
}

func FuzzSeedParallelFailure(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "seed-lifecycle" {
		f.Skip("parallel seed regression is not selected")
	}
	f.Add("parallel-failure")
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, _ string) {
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
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, _ string) {
		t.Log("ordinary seed for the non-selected fuzz target")
	})
}

func FuzzManagedSeeds(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "test-management" {
		f.Skip("test management regression is not selected")
	}
	f.Add(0)
	f.Add(1)
	f.Add(2)
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, seed int) {
		switch seed {
		case 0:
			panic("disabled seed executed")
		case 1:
			t.Error("quarantined seed failure")
		}
	})
}

func FuzzManagedDisabled(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "test-management" {
		f.Skip("test management regression is not selected")
	}
	panic("disabled fuzz target executed")
}

func FuzzManagedQuarantined(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "test-management" {
		f.Skip("test management regression is not selected")
	}
	f.Fatal("quarantined fuzz target failure")
}

func FuzzManagedAttemptToFix(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "test-management" {
		f.Skip("test management regression is not selected")
	}
	f.Add("pass")
	gotesting.GetFuzz(f).Fuzz(func(*testing.T, string) {})
}

func FuzzManagedQuarantinedGoexit(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "test-management" {
		f.Skip("test management regression is not selected")
	}
	runtime.Goexit()
}

var managedCombinedSeedRuns [4]atomic.Int32

func FuzzManagedCombinedSeeds(f *testing.F) {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") != "test-management" {
		f.Skip("test management regression is not selected")
	}
	for seed := range managedCombinedSeedRuns {
		f.Add(seed)
	}
	gotesting.GetFuzz(f).Fuzz(func(t *testing.T, seed int) {
		managedCombinedSeedRuns[seed].Add(1)
		if seed%2 != 0 {
			t.Error("managed combined seed failure")
		}
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
		panicWithValue(nil)
	}
	fmt.Println("safe")
	// Output: safe
}

func panicWithValue(value any) {
	panic(value)
}

func ExampleManagedDisabled() {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") == "test-management" {
		panic("disabled example executed")
	}
	fmt.Println("disabled expected")
	// Output: disabled expected
}

func ExampleManagedQuarantined() {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") == "test-management" {
		fmt.Println("quarantined actual")
		return
	}
	fmt.Println("quarantined expected")
	// Output: quarantined expected
}

func ExampleManagedQuarantinedPanic() {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") == "test-management" {
		panic("quarantined example panic")
	}
	fmt.Println("panic expected")
	// Output: panic expected
}

func ExampleManagedQuarantinedGoexit() {
	if os.Getenv("DD_FUZZ_EXAMPLE_SCENARIO") == "test-management" {
		runtime.Goexit()
	}
	fmt.Println("goexit expected")
	// Output: goexit expected
}

func ExampleManagedAttemptToFix() {
	fmt.Println("attempt expected")
	// Output: attempt expected
}

func ExampleWithoutOutput() {
	panic("an example without an output directive must not run")
}
