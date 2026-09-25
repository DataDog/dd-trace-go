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

func ExampleWithoutOutput() {
	panic("an example without an output directive must not run")
}
