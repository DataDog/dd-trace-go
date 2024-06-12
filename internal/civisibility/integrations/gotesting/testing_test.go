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
	"testing"

	ddhttp "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http"
	ddtracer "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// TestMain is the entry point for testing and runs before any test.
func TestMain(m *testing.M) {

	// (*M)(m).Run() cast m to gotesting.M and just run
	// or use a helper method gotesting.RunM(m)

	// os.Exit((*M)(m).Run())
	os.Exit(RunM(m))
}

// TestMyTest02 demonstrates instrumentation of InternalTests
func TestMyTest01(t *testing.T) {
}

// TestMyTest02 demonstrates instrumentation of sub-tests.
func TestMyTest02(gt *testing.T) {

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
func TestWithExternalCalls(oT *testing.T) {
	t := (*T)(oT)

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

		_, _ = client.Do(req)
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

		_, _ = client.Do(req)
	})
}

// TestSkip demonstrates skipping a test with a message.
func TestSkip(gt *testing.T) {
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
}
