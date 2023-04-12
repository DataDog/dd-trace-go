// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package namingschematest provides utilities to test naming schemas across different integrations.
package namingschematest

import (
	"testing"

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
func NewServiceNameTest(genSpans GenSpansFn, defaultName string, wantV0 ServiceNameAssertions) func(t *testing.T) {
	return func(t *testing.T) {
		testCases := []struct {
			name                string
			serviceNameOverride string
			ddService           string
			// wantV0 is a list because the expected service name for v0 could be different for each of the integration
			// generated spans.
			wantV0 []string
			wantV1 string
		}{
			{
				name:                "WithDefaults",
				serviceNameOverride: "",
				ddService:           "",
				wantV0:              wantV0.WithDefaults,
				wantV1:              defaultName,
			},
			{
				name:                "WithGlobalDDService",
				serviceNameOverride: "",
				ddService:           TestDDService,
				wantV0:              wantV0.WithDDService,
				wantV1:              TestDDService,
			},
			{
				name:                "WithGlobalDDServiceAndOverride",
				serviceNameOverride: TestServiceOverride,
				ddService:           TestDDService,
				wantV0:              wantV0.WithDDServiceAndOverride,
				wantV1:              TestServiceOverride,
			},
		}
		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				if tc.ddService != "" {
					svc := globalconfig.ServiceName()
					defer globalconfig.SetServiceName(svc)
					globalconfig.SetServiceName(tc.ddService)
				}

				t.Run("v0", func(t *testing.T) {
					version := namingschema.GetVersion()
					defer namingschema.SetVersion(version)
					namingschema.SetVersion(namingschema.SchemaV0)

					spans := genSpans(t, tc.serviceNameOverride)
					require.Len(t, spans, len(tc.wantV0), "the number of spans and number of assertions for v0 don't match")
					for i := 0; i < len(spans); i++ {
						assert.Equal(t, tc.wantV0[i], spans[i].Tag(ext.ServiceName))
					}
				})
				t.Run("v1", func(t *testing.T) {
					version := namingschema.GetVersion()
					defer namingschema.SetVersion(version)
					namingschema.SetVersion(namingschema.SchemaV1)

					spans := genSpans(t, tc.serviceNameOverride)
					for i := 0; i < len(spans); i++ {
						assert.Equal(t, tc.wantV1, spans[i].Tag(ext.ServiceName))
					}
				})
			})
		}
	}
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
