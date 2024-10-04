// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"slices"
	"strconv"
	"testing"

	ddhttp "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/utils/net"

	"github.com/stretchr/testify/assert"
)

var currentM *testing.M
var mTracer mocktracer.Tracer

// TestMain is the entry point for testing and runs before any test.
func TestMain(m *testing.M) {

	// mock the settings api to enable automatic test retries
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("MockApi received request: %s\n", r.URL.Path)

		// Settings request
		if r.URL.Path == "/api/v2/libraries/tests/services/setting" {
			w.Header().Set("Content-Type", "application/json")
			response := struct {
				Data struct {
					ID         string                   `json:"id"`
					Type       string                   `json:"type"`
					Attributes net.SettingsResponseData `json:"attributes"`
				} `json:"data,omitempty"`
			}{}

			// let's enable flaky test retries
			response.Data.Attributes = net.SettingsResponseData{
				FlakyTestRetriesEnabled: true,
			}

			fmt.Printf("MockApi sending response: %v\n", response)
			json.NewEncoder(w).Encode(&response)
		}
	}))
	defer server.Close()

	// set the custom agentless url and the flaky retry count env-var
	fmt.Printf("Using mockapi at: %s\n", server.URL)
	os.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "1")
	os.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, server.URL)
	os.Setenv(constants.APIKeyEnvironmentVariable, "12345")
	os.Setenv(constants.CIVisibilityFlakyRetryCountEnvironmentVariable, "10")

	// initialize the mock tracer for doing assertions on the finished spans
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	// execute the tests, because we are expecting some tests to fail and check the assertion later
	// we don't store the exit code from the test runner
	exitCode := RunM(m)
	if exitCode != 1 {
		panic("expected the exit code to be 1. We have a failing test on purpose.")
	}

	// get all finished spans
	finishedSpans := mTracer.FinishedSpans()

	// 1 session span
	// 1 module span
	// 2 suite span (testing_test.go and reflections_test.go)
	// 6 tests spans from testing_test.go
	// 7 sub stest spans from testing_test.go
	// 1 TestRetryWithPanic + 3 retry tests from testing_test.go
	// 1 TestRetryWithFail + 3 retry tests from testing_test.go
	// 1 TestRetryAlwaysFail + 10 retry tests from testing_test.go
	// 2 normal spans from testing_test.go
	// 5 tests from reflections_test.go
	// 2 benchmark spans (optional - require the -bench option)
	fmt.Printf("Number of spans received: %d\n", len(finishedSpans))
	if len(finishedSpans) < 37 {
		panic("expected at least 37 finished spans, got " + strconv.Itoa(len(finishedSpans)))
	}

	sessionSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSession)
	fmt.Printf("Number of sessions received: %d\n", len(sessionSpans))
	showResourcesNameFromSpans(sessionSpans)
	if len(sessionSpans) != 1 {
		panic("expected exactly 1 session span, got " + strconv.Itoa(len(sessionSpans)))
	}

	moduleSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestModule)
	fmt.Printf("Number of modules received: %d\n", len(moduleSpans))
	showResourcesNameFromSpans(moduleSpans)
	if len(moduleSpans) != 1 {
		panic("expected exactly 1 module span, got " + strconv.Itoa(len(moduleSpans)))
	}

	suiteSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSuite)
	fmt.Printf("Number of suites received: %d\n", len(suiteSpans))
	showResourcesNameFromSpans(suiteSpans)
	if len(suiteSpans) != 2 {
		panic("expected exactly 2 suite spans, got " + strconv.Itoa(len(suiteSpans)))
	}

	testSpans := getSpansWithType(finishedSpans, constants.SpanTypeTest)
	fmt.Printf("Number of tests received: %d\n", len(testSpans))
	showResourcesNameFromSpans(testSpans)
	if len(testSpans) != 37 {
		panic("expected exactly 37 test spans, got " + strconv.Itoa(len(testSpans)))
	}

	httpSpans := getSpansWithType(finishedSpans, ext.SpanTypeHTTP)
	fmt.Printf("Number of http spans received: %d\n", len(httpSpans))
	showResourcesNameFromSpans(httpSpans)
	if len(httpSpans) != 2 {
		panic("expected exactly 2 normal spans, got " + strconv.Itoa(len(httpSpans)))
	}

	os.Exit(0)
}

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
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Log(test.name)
			buf = append(buf, test.index)
		})
	}

	expected := []byte{1, 2, 3}
	if !slices.Equal(buf, expected) {
		t.Error("error in subtests closure")
	}
}

// TestWithExternalCalls demonstrates testing with external HTTP calls.
func TestWithExternalCalls(gt *testing.T) {
	assertTest(gt)
	t := (*T)(gt)

	// Create a new HTTP test server
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("Hello World"))
	}))
	defer s.Close()

	t.Run("default", func(t *testing.T) {

		// if we want to use the test span as a parent of a child span
		// we can extract the SpanContext and use it in other integrations
		ctx := (*T)(t).Context()

		// Wrap the default HTTP transport for tracing
		rt := ddhttp.WrapRoundTripper(http.DefaultTransport)
		client := &http.Client{
			Transport: rt,
		}

		// Create a new HTTP request
		req, err := http.NewRequest("GET", s.URL+"/hello/world", nil)
		if err != nil {
			t.FailNow()
		}

		// Use the span context here so the http span will appear as a child of the test
		req = req.WithContext(ctx)

		res, err := client.Do(req)
		if err != nil {
			t.FailNow()
		}
		_ = res.Body.Close()
	})

	t.Run("custom-name", func(t *testing.T) {

		// we can also add custom tags to the test span by retrieving the
		// context and call the `ddtracer.SpanFromContext` api
		ctx := (*T)(t).Context()
		span, _ := ddtracer.SpanFromContext(ctx)

		// Custom namer function for the HTTP request
		customNamer := func(req *http.Request) string {
			value := fmt.Sprintf("%s %s", req.Method, req.URL.Path)

			// Then we can set custom tags to that test span
			span.SetTag("customNamer.Value", value)
			return value
		}

		rt := ddhttp.WrapRoundTripper(http.DefaultTransport, ddhttp.RTWithResourceNamer(customNamer))
		client := &http.Client{
			Transport: rt,
		}

		req, err := http.NewRequest("GET", s.URL+"/hello/world", nil)
		if err != nil {
			t.FailNow()
		}

		// Use the span context here so the http span will appear as a child of the test
		req = req.WithContext(ctx)

		res, err := client.Do(req)
		if err != nil {
			t.FailNow()
		}
		_ = res.Body.Close()
	})
}

// TestSkip demonstrates skipping a test with a message.
func TestSkip(gt *testing.T) {
	assertTest(gt)

	t := (*T)(gt)

	// because we use the instrumented Skip
	// the message will be reported as the skip reason.
	t.Skip("Nothing to do here, skipping!")
}

// Tests for test retries feature

var testRetryWithPanicRunNumber = 0

func TestRetryWithPanic(t *testing.T) {
	t.Cleanup(func() {
		if testRetryWithPanicRunNumber == 1 {
			fmt.Println("CleanUp from the initial execution")
		} else {
			fmt.Println("CleanUp from the retry")
		}
	})
	testRetryWithPanicRunNumber++
	if testRetryWithPanicRunNumber < 4 {
		panic("Test Panic")
	}
}

var testRetryWithFailRunNumber = 0

func TestRetryWithFail(t *testing.T) {
	t.Cleanup(func() {
		if testRetryWithFailRunNumber == 1 {
			fmt.Println("CleanUp from the initial execution")
		} else {
			fmt.Println("CleanUp from the retry")
		}
	})
	testRetryWithFailRunNumber++
	if testRetryWithFailRunNumber < 4 {
		t.Fatal("Failed due the wrong execution number")
	}
}

func TestRetryAlwaysFail(t *testing.T) {
	t.Parallel()
	t.Fatal("Always fail")
}

func TestNormalPassingAfterRetryAlwaysFail(t *testing.T) {}

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

func assertTest(t *testing.T) {
	assert := assert.New(t)
	spans := mTracer.OpenSpans()
	hasSession := false
	hasModule := false
	hasSuite := false
	hasTest := false
	for _, span := range spans {
		spanTags := span.Tags()

		// Assert Session
		if span.Tag(ext.SpanType) == constants.SpanTypeTestSession {
			assert.Contains(spanTags, constants.TestSessionIDTag)
			assertCommon(assert, span)
			hasSession = true
		}

		// Assert Module
		if span.Tag(ext.SpanType) == constants.SpanTypeTestModule {
			assert.Subset(spanTags, map[string]interface{}{
				constants.TestModule:    "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting",
				constants.TestFramework: "golang.org/pkg/testing",
			})
			assert.Contains(spanTags, constants.TestSessionIDTag)
			assert.Contains(spanTags, constants.TestModuleIDTag)
			assert.Contains(spanTags, constants.TestFrameworkVersion)
			assertCommon(assert, span)
			hasModule = true
		}

		// Assert Suite
		if span.Tag(ext.SpanType) == constants.SpanTypeTestSuite {
			assert.Subset(spanTags, map[string]interface{}{
				constants.TestModule:    "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting",
				constants.TestFramework: "golang.org/pkg/testing",
			})
			assert.Contains(spanTags, constants.TestSessionIDTag)
			assert.Contains(spanTags, constants.TestModuleIDTag)
			assert.Contains(spanTags, constants.TestSuiteIDTag)
			assert.Contains(spanTags, constants.TestFrameworkVersion)
			assert.Contains(spanTags, constants.TestSuite)
			assertCommon(assert, span)
			hasSuite = true
		}

		// Assert Test
		if span.Tag(ext.SpanType) == constants.SpanTypeTest {
			assert.Subset(spanTags, map[string]interface{}{
				constants.TestModule:    "gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations/gotesting",
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
			assertCommon(assert, span)
			hasTest = true
		}
	}

	assert.True(hasSession)
	assert.True(hasModule)
	assert.True(hasSuite)
	assert.True(hasTest)
}

func assertCommon(assert *assert.Assertions, span mocktracer.Span) {
	spanTags := span.Tags()

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

func getSpansWithType(spans []mocktracer.Span, spanType string) []mocktracer.Span {
	var result []mocktracer.Span
	for _, span := range spans {
		if span.Tag(ext.SpanType) == spanType {
			result = append(result, span)
		}
	}

	return result
}

func showResourcesNameFromSpans(spans []mocktracer.Span) {
	for i, span := range spans {
		fmt.Printf("  [%d] = %v\n", i, span.Tag(ext.ResourceName))
	}
}
