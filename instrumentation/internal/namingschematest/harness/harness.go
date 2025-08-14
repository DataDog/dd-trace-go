// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package harness

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const (
	TestDDService       = "dd-service"
	TestServiceOverride = "service-override"
)

type AssertSpansFn func(t *testing.T, spans []*mocktracer.Span)

type ServiceNameAssertions struct {
	Defaults        []string
	DDService       []string
	ServiceOverride []string
}

type GenSpansFn func(t *testing.T, serviceOverride string) []*mocktracer.Span

type TestCase struct {
	Name              instrumentation.Package
	GenSpans          GenSpansFn
	WantServiceNameV0 ServiceNameAssertions
	AssertOpV0        AssertSpansFn
	AssertOpV1        AssertSpansFn
}

func RunTest(t *testing.T, tc TestCase) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	}
	t.Run(strings.ReplaceAll(string(tc.Name), "/", "_"), func(t *testing.T) {
		t.Run("ServiceName", func(t *testing.T) {
			// v0
			t.Run("v0_defaults", func(t *testing.T) {
				t.Setenv("DD_SERVICE", "")
				t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
				instrumentation.ReloadConfig()
				spans := tc.GenSpans(t, "")
				assertServiceNames(t, spans, tc.WantServiceNameV0.Defaults)
			})
			t.Run("v0_dd_service", func(t *testing.T) {
				t.Setenv("DD_SERVICE", TestDDService)
				t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
				instrumentation.ReloadConfig()
				spans := tc.GenSpans(t, "")
				assertServiceNames(t, spans, tc.WantServiceNameV0.DDService)
			})
			t.Run("v0_dd_service_and_override", func(t *testing.T) {
				t.Setenv("DD_SERVICE", TestDDService)
				t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
				instrumentation.ReloadConfig()
				spans := tc.GenSpans(t, TestServiceOverride)
				assertServiceNames(t, spans, tc.WantServiceNameV0.ServiceOverride)
			})
			t.Run("v0_dd_service_remove_integration_service_names", func(t *testing.T) {
				t.Setenv("DD_SERVICE", TestDDService)
				t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
				t.Setenv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", "true")
				instrumentation.ReloadConfig()
				spans := tc.GenSpans(t, "")
				// in this setup, we should always have DD_SERVICE even if using schema v0
				assertServiceNames(t, spans, RepeatString(TestDDService, len(tc.WantServiceNameV0.DDService)))
			})
			t.Run("v0_dd_service_remove_integration_service_names_tracer_option", func(t *testing.T) {
				t.Setenv("DD_SERVICE", TestDDService)
				t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
				instrumentation.ReloadConfig()
				// this option is equivalent to setting the environment variable DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED
				tracer.WithGlobalServiceName(true)(nil)

				spans := tc.GenSpans(t, "")
				// in this setup, we should always have DD_SERVICE even if using schema v0
				assertServiceNames(t, spans, RepeatString(TestDDService, len(tc.WantServiceNameV0.DDService)))
			})

			// v1
			t.Run("v1_defaults", func(t *testing.T) {
				t.Setenv("DD_SERVICE", "")
				t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
				instrumentation.ReloadConfig()
				spans := tc.GenSpans(t, "")
				assertServiceNames(t, spans, tc.WantServiceNameV0.Defaults)
			})
			t.Run("v1_dd_service", func(t *testing.T) {
				t.Setenv("DD_SERVICE", TestDDService)
				t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
				instrumentation.ReloadConfig()
				spans := tc.GenSpans(t, "")
				assertServiceNames(t, spans, RepeatString(TestDDService, len(tc.WantServiceNameV0.DDService)))
			})
			t.Run("v1_dd_service_and_override", func(t *testing.T) {
				t.Setenv("DD_SERVICE", TestDDService)
				t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
				instrumentation.ReloadConfig()
				spans := tc.GenSpans(t, TestServiceOverride)
				assertServiceNames(t, spans, RepeatString(TestServiceOverride, len(tc.WantServiceNameV0.ServiceOverride)))
			})
		})

		t.Run("SpanName", func(t *testing.T) {
			t.Run("v0", func(t *testing.T) {
				t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
				instrumentation.ReloadConfig()
				spans := tc.GenSpans(t, "")
				tc.AssertOpV0(t, spans)
			})
			t.Run("v1", func(t *testing.T) {
				t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
				instrumentation.ReloadConfig()
				spans := tc.GenSpans(t, "")
				tc.AssertOpV1(t, spans)
			})
		})
	})
}

func assertServiceNames(t *testing.T, spans []*mocktracer.Span, wantServiceNames []string) {
	t.Helper()
	require.Len(t, spans, len(wantServiceNames), "the number of spans and number of assertions should be the same")
	for i := 0; i < len(spans); i++ {
		want, got, spanName := wantServiceNames[i], spans[i].Tag(ext.ServiceName), spans[i].OperationName()
		if want == "" {
			assert.Empty(t, got, "expected empty service name tag for span: %s", spanName)
		} else {
			assert.Equal(t, want, got, "incorrect service name for span: %s", spanName)
		}
	}
}
