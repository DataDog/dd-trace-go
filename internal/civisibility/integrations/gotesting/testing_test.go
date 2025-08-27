// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"

	"github.com/stretchr/testify/assert"
)

// TestMyTest01 demonstrates instrumentation of InternalTests
func TestMyTest01(t *testing.T) {
	assertTest(t)
}

// TestMyTest02 demonstrates instrumentation of subtests.
func TestMyTest02(gt *testing.T) {
	assertTest(gt)

	// To instrument subTests we just need to cast
	// testing.T to our gotesting.T
	// using: newT := (*gotesting.T)(t)
	// Then all testing.T will be available but instrumented
	t := (*T)(gt)
	// or
	t = GetTest(gt)

	t.Run("sub01", func(oT2 *testing.T) {
		t2 := (*T)(oT2) // Cast the sub-test to gotesting.T
		t2.Log("From sub01")
		t2.Run("sub03", func(t3 *testing.T) {
			t3.Log("From sub03")
		})
	})
}

func Test_Foo(gt *testing.T) {
	assertTest(gt)
	t := (*T)(gt)

	var tests = []struct {
		index byte
		name  string
		input string
		want  string
	}{
		{1, "yellow should return color", "yellow", "color"},
		{2, "banana should return fruit", "banana", "fruit"},
		{3, "duck should return animal", "duck", "animal"},
	}
	buf := []byte{}
	for _, test := range tests {
		mT := test
		t.Run(mT.name, func(t *testing.T) {
			// let's run the subtest in parallel
			t.Log(mT.name)
			buf = append(buf, mT.index)
		})
	}

	expected := []byte{1, 2, 3}
	if !slices.Equal(buf, expected) {
		t.Error("error in subtests closure")
	}
}

// TestSkip demonstrates skipping a test with a message.
func TestSkip(gt *testing.T) {
	assertTest(gt)

	// To instrument skip tests we just need to cast
	t := (*T)(gt)

	// because we use the instrumented Skip
	// the message will be reported as the skip reason.
	t.Skip("Nothing to do here, skipping!")
}

func TestParallelSubTests(gt *testing.T) {
	assertTest(gt)

	// To instrument parallel sub-tests we just need to cast
	t := (*T)(gt)

	t.Run("parallel_subtest_1", func(t *testing.T) {
		t.Parallel()
		<-time.After(300 * time.Millisecond) // Simulate some work
		fmt.Println("Running parallel subtest 1")
	})

	t.Run("parallel_subtest_2", func(t *testing.T) {
		t.Parallel()
		<-time.After(200 * time.Millisecond) // Simulate some work
		fmt.Println("Running parallel subtest 2")
	})

	t.Run("parallel_subtest_3", func(t *testing.T) {
		t.Parallel()
		<-time.After(100 * time.Millisecond) // Simulate some work
		fmt.Println("Running parallel subtest 3")
	})
}

// Tests for test retries feature

var testRetryWithPanicRunNumber atomic.Int32

func TestRetryWithPanic(t *testing.T) {
	t.Cleanup(func() {
		if testRetryWithPanicRunNumber.Load() == 1 {
			fmt.Println("CleanUp from the initial execution")
		} else {
			fmt.Println("CleanUp from the retry")
		}
	})

	if testRetryWithPanicRunNumber.Add(1) < 4 {
		panic("Test Panic")
	}
}

var testRetryWithFailRunNumber atomic.Int32

func TestRetryWithFail(t *testing.T) {
	t.Cleanup(func() {
		if testRetryWithFailRunNumber.Load() == 1 {
			fmt.Println("CleanUp from the initial execution")
		} else {
			fmt.Println("CleanUp from the retry")
		}
	})

	if testRetryWithFailRunNumber.Add(1) < 4 {
		t.Fatal("Failed due the wrong execution number")
	}
}

//dd:test.unskippable
func TestNormalPassingAfterRetryAlwaysFail(_ *testing.T) {}

var run atomic.Int32

//dd:test.unskippable
func TestEarlyFlakeDetection(t *testing.T) {
	runValue := run.Add(1)
	if os.Getenv(constants.CIVisibilityInternalParallelEarlyFlakeDetectionEnabled) == "true" {
		<-time.After(4 * time.Second)
	}
	fmt.Printf(" Run: %d", runValue)
	if runValue%2 == 0 {
		fmt.Println(" Failed")
		t.FailNow()
	}
	fmt.Println(" Passed")
}

// BenchmarkFirst demonstrates benchmark instrumentation with sub-benchmarks.
func BenchmarkFirst(gb *testing.B) {

	// Same happens with sub benchmarks
	// we just need to cast testing.B to gotesting.B
	// using: newB := (*gotesting.B)(b)
	b := (*B)(gb)
	// or
	b = GetBenchmark(gb)

	var mapArray []map[string]string
	b.Run("child01", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			mapArray = append(mapArray, map[string]string{})
		}
	})

	b.Run("child02", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			mapArray = append(mapArray, map[string]string{})
		}
	})

	b.Run("child03", func(b *testing.B) {
		GetBenchmark(b).Skip("The reason...")
	})

	_ = gb.Elapsed()
}

var assertMutex sync.Mutex

func assertTest(t *testing.T) {
	// we don't assert on parallel efd tests
	if parallelEfd {
		return
	}
	assertMutex.Lock()
	defer assertMutex.Unlock()
	assert := assert.New(t)
	spans := mTracer.OpenSpans()
	hasSession := false
	hasModule := false
	hasSuite := false
	hasTest := false

	assertCommon := func(spanTags map[string]interface{}) {
		assert.Subset(spanTags, map[string]interface{}{
			constants.Origin:          constants.CIAppTestOrigin,
			constants.TestType:        constants.TestTypeTest,
			constants.LogicalCPUCores: float64(runtime.NumCPU()),
		})

		assert.Contains(spanTags, ext.ResourceName)
		assert.Contains(spanTags, constants.TestCommand)
		assert.Contains(spanTags, constants.TestCommandWorkingDirectory)
		assert.Contains(spanTags, constants.OSPlatform)
		assert.Contains(spanTags, constants.OSArchitecture)
		assert.Contains(spanTags, constants.OSVersion)
		assert.Contains(spanTags, constants.RuntimeVersion)
		assert.Contains(spanTags, constants.RuntimeName)
		assert.Contains(spanTags, constants.GitRepositoryURL)
		assert.Contains(spanTags, constants.GitCommitSHA)
		// GitHub CI does not provide commit details
		if spanTags[constants.CIProviderName] != "github" {
			assert.Contains(spanTags, constants.GitCommitMessage)
			assert.Contains(spanTags, constants.GitCommitAuthorEmail)
			assert.Contains(spanTags, constants.GitCommitAuthorDate)
			assert.Contains(spanTags, constants.GitCommitCommitterEmail)
			assert.Contains(spanTags, constants.GitCommitCommitterDate)
			assert.Contains(spanTags, constants.GitCommitCommitterName)
		}
		assert.Contains(spanTags, constants.CIWorkspacePath)
	}

	for _, span := range spans {
		spanTags := span.Tags()

		// Assert Session
		if spanTags[ext.SpanType] == constants.SpanTypeTestSession {
			assert.Subset(spanTags, map[string]interface{}{
				constants.TestFramework: "golang.org/pkg/testing",
			})
			assert.Contains(spanTags, constants.TestSessionIDTag)
			assertCommon(spanTags)
			hasSession = true
		}

		// Assert Module
		if spanTags[ext.SpanType] == constants.SpanTypeTestModule {
			assert.Subset(spanTags, map[string]interface{}{
				constants.TestModule:    "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting",
				constants.TestFramework: "golang.org/pkg/testing",
			})
			assert.Contains(spanTags, constants.TestSessionIDTag)
			assert.Contains(spanTags, constants.TestModuleIDTag)
			assert.Contains(spanTags, constants.TestFrameworkVersion)
			assertCommon(spanTags)
			hasModule = true
		}

		// Assert Suite
		if spanTags[ext.SpanType] == constants.SpanTypeTestSuite {
			assert.Subset(spanTags, map[string]interface{}{
				constants.TestModule:    "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting",
				constants.TestFramework: "golang.org/pkg/testing",
			})
			assert.Contains(spanTags, constants.TestSessionIDTag)
			assert.Contains(spanTags, constants.TestModuleIDTag)
			assert.Contains(spanTags, constants.TestSuiteIDTag)
			assert.Contains(spanTags, constants.TestFrameworkVersion)
			assert.Contains(spanTags, constants.TestSuite)
			assertCommon(spanTags)
			hasSuite = true
		}

		// Assert Test
		if spanTags[ext.SpanType] == constants.SpanTypeTest {
			assert.Subset(spanTags, map[string]interface{}{
				constants.TestModule:    "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting",
				constants.TestFramework: "golang.org/pkg/testing",
				constants.TestSuite:     "testing_test.go",
				constants.TestName:      t.Name(),
				constants.TestType:      constants.TestTypeTest,
			})
			assert.Contains(spanTags, constants.TestSessionIDTag)
			assert.Contains(spanTags, constants.TestModuleIDTag)
			assert.Contains(spanTags, constants.TestSuiteIDTag)
			assert.Contains(spanTags, constants.TestFrameworkVersion)
			assert.Contains(spanTags, constants.TestCodeOwners)
			assert.Contains(spanTags, constants.TestSourceFile)
			assert.Contains(spanTags, constants.TestSourceStartLine)
			assertCommon(spanTags)
			hasTest = true
		}
	}

	assert.True(hasSession)
	assert.True(hasModule)
	assert.True(hasSuite)
	assert.True(hasTest)
}
