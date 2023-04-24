// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package namingschematest provides utilities to test naming schemas across different integrations.
package namingschematest

import (
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/lists"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GenSpansFn is used across different functions from this package to generate spans. It should be implemented in the
// tests that use this package.
// The provided serviceOverride string should be used to set the specific integration's WithServiceName option (if
// available) when initializing and configuring the package.
type GenSpansFn func(t *testing.T, serviceOverride string) []mocktracer.Span

// ServiceNameAssertions contains assertions for different test cases used inside the generated test
// from NewServiceNameTest.
// []string fields in this struct represent the assertions to be made against the returned []mocktracer.Span from
// GenSpansFn in the same order.
type ServiceNameAssertions struct {
	// WithDefaults is used for the test case where defaults are used.
	WithDefaults []string
	// WithDDService is used when the global DD_SERVICE configuration is enabled (in this case, the test will set the
	// value to TestDDService from this package).
	WithDDService []string
	// WithDDServiceAndOverride is used for the test case where the global DD_SERVICE configuration is enabled and the
	// contrib specific `WithServiceName` option is used (in this case, the test will set DD_SERVICE and serviceOverride
	// to the TestDDService and TestServiceOverride constants from this package, respectively).
	WithDDServiceAndOverride []string
}

const (
	// TestDDService is a constant used in the test returned by NewServiceNameTest to set the value of DD_SERVICE.
	TestDDService = "dd-service"
	// TestServiceOverride is a constant used in the test returned by NewServiceNameTest to set the value of
	// the integration's WithServiceName option.
	TestServiceOverride = "service-override"
)

// NewServiceNameTest generates a new test for span service names using the naming schema versioning.
// TODO(rarguelloF): remove the 2nd parameter as it's unused.
func NewServiceNameTest(genSpans GenSpansFn, _ string, wantV0 ServiceNameAssertions) func(t *testing.T) {
	return func(t *testing.T) {
		testCases := []struct {
			name                string
			serviceNameOverride string
			ddService           string
			// the assertions are a slice that should match the number of spans returned by the genSpans function.
			wantV0 []string
			wantV1 []string
		}{
			{
				name:                "WithDefaults",
				serviceNameOverride: "",
				ddService:           "",
				wantV0:              wantV0.WithDefaults,
				wantV1:              wantV0.WithDefaults, // defaults should be the same for v1
			},
			{
				name:                "WithGlobalDDService",
				serviceNameOverride: "",
				ddService:           TestDDService,
				wantV0:              wantV0.WithDDService,
				wantV1:              lists.RepeatString(TestDDService, len(wantV0.WithDDService)),
			},
			{
				name:                "WithGlobalDDServiceAndOverride",
				serviceNameOverride: TestServiceOverride,
				ddService:           TestDDService,
				wantV0:              wantV0.WithDDServiceAndOverride,
				wantV1:              lists.RepeatString(TestServiceOverride, len(wantV0.WithDDServiceAndOverride)),
			},
		}
		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				if tc.ddService != "" {
					reset := withDDService(tc.ddService)
					defer reset()
				}
				t.Run("v0", func(t *testing.T) {
					reset := withNamingSchemaVersion(namingschema.SchemaV0)
					defer reset()
					spans := genSpans(t, tc.serviceNameOverride)
					assertServiceNames(t, spans, tc.wantV0)
				})
				t.Run("v1", func(t *testing.T) {
					reset := withNamingSchemaVersion(namingschema.SchemaV1)
					defer reset()
					spans := genSpans(t, tc.serviceNameOverride)
					assertServiceNames(t, spans, tc.wantV1)
				})
			})
		}
	}
}

func assertServiceNames(t *testing.T, spans []mocktracer.Span, wantServiceNames []string) {
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

func withNamingSchemaVersion(version namingschema.Version) func() {
	prevVersion := namingschema.GetVersion()
	reset := func() { namingschema.SetVersion(prevVersion) }
	namingschema.SetVersion(version)
	return reset
}

func withDDService(ddService string) func() {
	prevName := globalconfig.ServiceName()
	reset := func() { globalconfig.SetServiceName(prevName) }
	globalconfig.SetServiceName(ddService)
	return reset
}

// AssertSpansFn allows to make assertions on the generated spans.
type AssertSpansFn func(t *testing.T, spans []mocktracer.Span)

// NewOpNameTest returns a new test that runs the provided assertion functions for each schema version.
func NewOpNameTest(genSpans GenSpansFn, assertV0 AssertSpansFn, assertV1 AssertSpansFn) func(t *testing.T) {
	return func(t *testing.T) {
		t.Run("v0", func(t *testing.T) {
			version := namingschema.GetVersion()
			defer namingschema.SetVersion(version)
			namingschema.SetVersion(namingschema.SchemaV0)

			spans := genSpans(t, "")
			assertV0(t, spans)
		})
		t.Run("v1", func(t *testing.T) {
			version := namingschema.GetVersion()
			defer namingschema.SetVersion(version)
			namingschema.SetVersion(namingschema.SchemaV1)

			spans := genSpans(t, "")
			assertV1(t, spans)
		})
	}
}
