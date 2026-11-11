// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"flag"
	"reflect"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils"
)

func TestRestoreTestingMWorkloadsRestoresFuzzTargetsAndExamples(t *testing.T) {
	originalFuzz := func(*testing.F) {}
	originalExample := func() {}
	fuzzTargets := []testing.InternalFuzzTarget{{Name: "FuzzOriginal", Fn: func(*testing.F) {}}}
	examples := []testing.InternalExample{{Name: "ExampleOriginal", F: func() {}, Output: "output\n", Unordered: true}}
	claim := &testingMInstrumentationClaim{
		fuzzDescriptors:    &fuzzTargets,
		exampleDescriptors: &examples,
		fuzzTargets:        map[string]func(*testing.F){"FuzzOriginal": originalFuzz},
		examples:           map[string]func(){"ExampleOriginal": originalExample},
	}

	restoreTestingMWorkloads(&testing.M{}, claim)

	require.Equal(t, reflectFuncPointer(originalFuzz), reflectFuncPointer(fuzzTargets[0].Fn))
	require.Equal(t, reflectFuncPointer(originalExample), reflectFuncPointer(examples[0].F))
	require.Equal(t, "output\n", examples[0].Output)
	require.True(t, examples[0].Unordered)
}

func TestFuzzAndExampleDescriptorsReserveWorkloadCounters(t *testing.T) {
	fuzzFunc := func(*testing.F) {}
	exampleFunc := func() {}
	fuzzTargets := []testing.InternalFuzzTarget{{Name: "FuzzCounter", Fn: fuzzFunc}}
	examples := []testing.InternalExample{{Name: "ExampleCounter", F: exampleFunc, Output: "output\n"}}

	type counterDelta struct {
		modules map[string]int
		suites  map[string]int
	}
	want := counterDelta{modules: map[string]int{}, suites: map[string]int{}}
	for _, fn := range []any{fuzzFunc, exampleFunc} {
		function := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
		moduleName, suiteName := utils.GetModuleAndSuiteName(function.Entry())
		want.modules[moduleName]++
		want.suites[suiteName]++
	}
	moduleCountersBefore := make(map[string]int, len(want.modules))
	for name := range want.modules {
		moduleCountersBefore[name] = addModulesCounters(name, 0)
	}
	suiteCountersBefore := make(map[string]int, len(want.suites))
	for name := range want.suites {
		suiteCountersBefore[name] = addSuitesCounters(name, 0)
	}
	t.Cleanup(func() {
		for name, delta := range want.modules {
			addModulesCounters(name, -delta)
		}
		for name, delta := range want.suites {
			addSuitesCounters(name, -delta)
		}
	})

	ddm := new(M)
	ddm.instrumentInternalFuzzTargets(&fuzzTargets, nil)
	ddm.instrumentInternalExamples(&examples, nil)

	for name, delta := range want.modules {
		require.Equal(t, moduleCountersBefore[name]+delta, addModulesCounters(name, 0))
	}
	for name, delta := range want.suites {
		require.Equal(t, suiteCountersBefore[name]+delta, addSuitesCounters(name, 0))
	}
}

func TestInstrumentedAllWorkloads(t *testing.T) {
	// flag.Set marks a flag as set for good, so use a scratch command line.
	commandLine := flag.CommandLine
	t.Cleanup(func() { flag.CommandLine = commandLine })
	flag.CommandLine = flag.NewFlagSet(commandLine.Name(), flag.ContinueOnError)
	flag.String("test.bench", "", "")

	var (
		tests       []testing.InternalTest
		benchmarks  []testing.InternalBenchmark
		fuzzTargets []testing.InternalFuzzTarget
		examples    []testing.InternalExample
	)
	claim := &testingMInstrumentationClaim{testDescriptors: &tests, fuzzDescriptors: &fuzzTargets, exampleDescriptors: &examples}
	require.True(t, claim.instrumentedAllWorkloads())

	// Benchmarks selected outside os.Args run unwrapped, even if the flag is
	// cleared again before M.Run returns.
	require.NoError(t, flag.Set("test.bench", "BenchmarkSelected"))
	require.False(t, claim.instrumentedAllWorkloads())
	require.NoError(t, flag.Set("test.bench", ""))
	require.False(t, claim.instrumentedAllWorkloads())
	claim.benchmarkDescriptors = &benchmarks
	require.True(t, claim.instrumentedAllWorkloads())

	// Examples run natively when they cannot be wrapped, as on js and wasip1.
	claim.exampleDescriptors = nil
	require.False(t, claim.instrumentedAllWorkloads())
}

func reflectFuncPointer(fn any) uintptr {
	return reflect.ValueOf(fn).Pointer()
}
