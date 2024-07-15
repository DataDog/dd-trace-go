// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	ddhttp "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/constants"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/civisibility/integrations"

	"github.com/stretchr/testify/assert"
)

var currentM *testing.M
var mTracer mocktracer.Tracer

// TestMain is the entry point for testing and runs before any test.
func TestMain(m *testing.M) {
	currentM = m
	mTracer = integrations.InitializeCIVisibilityMock()

	// (*M)(m).Run() cast m to gotesting.M and just run
	// or use a helper method gotesting.RunM(m)

	// os.Exit((*M)(m).Run())
	_ = RunM(m)

	finishedSpans := mTracer.FinishedSpans()
	// 1 session span
	// 1 module span
	// 1 suite span (optional 1 from reflections_test.go)
	// 6 tests spans
	// 7 sub stest spans
	// 2 normal spans (from integration tests)
	// 1 benchmark span (optional - require the -bench option)
	if len(finishedSpans) < 17 {
		panic("expected at least 17 finished spans, got " + strconv.Itoa(len(finishedSpans)))
	}

	sessionSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSession)
	if len(sessionSpans) != 1 {
		panic("expected exactly 1 session span, got " + strconv.Itoa(len(sessionSpans)))
	}

	moduleSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestModule)
	if len(moduleSpans) != 1 {
		panic("expected exactly 1 module span, got " + strconv.Itoa(len(moduleSpans)))
	}

	suiteSpans := getSpansWithType(finishedSpans, constants.SpanTypeTestSuite)
	if len(suiteSpans) < 1 {
		panic("expected at least 1 suite span, got " + strconv.Itoa(len(suiteSpans)))
	}

	testSpans := getSpansWithType(finishedSpans, constants.SpanTypeTest)
	if len(testSpans) < 12 {
		panic("expected at least 12 suite span, got " + strconv.Itoa(len(testSpans)))
	}

	httpSpans := getSpansWithType(finishedSpans, ext.SpanTypeHTTP)
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
		name  string
		input string
		want  string
	}{
		{"yellow should return color", "yellow", "color"},
		{"banana should return fruit", "banana", "fruit"},
		{"duck should return animal", "duck", "animal"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Log(test.name)
		})
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
		constants.Origin:   constants.CIAppTestOrigin,
		constants.TestType: constants.TestTypeTest,
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
