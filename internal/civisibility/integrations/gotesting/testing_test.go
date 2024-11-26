// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package gotesting

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"slices"
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
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

// Code inspired by contrib/net/http/roundtripper.go
// It's not possible to import `contrib/net/http` package because it causes a circular dependency.
// This is a simplified version of the code.
type roundTripper struct {
	base  http.RoundTripper
	namer func(*http.Request) string
}

func (rt *roundTripper) RoundTrip(req *http.Request) (res *http.Response, err error) {
	resourceName := rt.namer(req)
	// Make a copy of the URL so we don't modify the outgoing request
	url := *req.URL
	url.User = nil // Do not include userinfo in the HTTPURL tag.
	opts := []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeHTTP),
		tracer.ResourceName(resourceName),
		tracer.Tag(ext.HTTPMethod, req.Method),
		tracer.Tag(ext.HTTPURL, url.String()),
		tracer.Tag(ext.SpanKind, ext.SpanKindClient),
		tracer.Tag(ext.NetworkDestinationName, url.Hostname()),
	}
	span, ctx := tracer.StartSpanFromContext(req.Context(), "", opts...)
	defer func() {
		span.Finish()
	}()
	r2 := req.Clone(ctx)
	res, err = rt.base.RoundTrip(r2)
	if err != nil {
		span.SetTag("http.errors", err.Error())
	} else {
		span.SetTag(ext.HTTPCode, strconv.Itoa(res.StatusCode))
		// treat 5XX as errors
		if res.StatusCode/100 == 5 {
			span.SetTag("http.errors", res.Status)
			span.SetTag(ext.Error, fmt.Errorf("%d: %s", res.StatusCode, http.StatusText(res.StatusCode)))
		}
	}
	return res, err
}

// Code from contrib/net/http/roundtripper.go
// It's not possible to import `contrib/net/http` package because it causes a circular dependency.
func wrapRoundTripper(rt http.RoundTripper, namer func(*http.Request) string) http.RoundTripper {
	if namer == nil {
		namer = func(req *http.Request) string {
			return ""
		}
	}
	return &roundTripper{
		base:  rt,
		namer: namer,
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
		rt := wrapRoundTripper(http.DefaultTransport, nil)
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
		span, _ := tracer.SpanFromContext(ctx)

		// Custom namer function for the HTTP request
		customNamer := func(req *http.Request) string {
			value := fmt.Sprintf("%s %s", req.Method, req.URL.Path)

			// Then we can set custom tags to that test span
			span.SetTag("customNamer.Value", value)
			return value
		}

		rt := wrapRoundTripper(http.DefaultTransport, customNamer)
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

//dd:test.unskippable
func TestNormalPassingAfterRetryAlwaysFail(t *testing.T) {}

var run int

//dd:test.unskippable
func TestEarlyFlakeDetection(t *testing.T) {
	run++
	fmt.Printf(" Run: %d", run)
	if run%2 == 0 {
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
			assert.Subset(spanTags, map[string]interface{}{
				constants.TestFramework: "golang.org/pkg/testing",
			})
			assert.Contains(spanTags, constants.TestSessionIDTag)
			assertCommon(assert, *span)
			hasSession = true
		}

		// Assert Module
		if span.Tag(ext.SpanType) == constants.SpanTypeTestModule {
			assert.Subset(spanTags, map[string]interface{}{
				constants.TestModule:    "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting",
				constants.TestFramework: "golang.org/pkg/testing",
			})
			assert.Contains(spanTags, constants.TestSessionIDTag)
			assert.Contains(spanTags, constants.TestModuleIDTag)
			assert.Contains(spanTags, constants.TestFrameworkVersion)
			assertCommon(assert, *span)
			hasModule = true
		}

		// Assert Suite
		if span.Tag(ext.SpanType) == constants.SpanTypeTestSuite {
			assert.Subset(spanTags, map[string]interface{}{
				constants.TestModule:    "github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations/gotesting",
				constants.TestFramework: "golang.org/pkg/testing",
			})
			assert.Contains(spanTags, constants.TestSessionIDTag)
			assert.Contains(spanTags, constants.TestModuleIDTag)
			assert.Contains(spanTags, constants.TestSuiteIDTag)
			assert.Contains(spanTags, constants.TestFrameworkVersion)
			assert.Contains(spanTags, constants.TestSuite)
			assertCommon(assert, *span)
			hasSuite = true
		}

		// Assert Test
		if span.Tag(ext.SpanType) == constants.SpanTypeTest {
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
			assertCommon(assert, *span)
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
