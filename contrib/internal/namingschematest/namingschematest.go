// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

// Package namingschematest provides utilities to test naming schemas across different integrations.
package namingschematest

import (
	"testing"

	v2mock "github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
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
func NewServiceNameTest(genSpans GenSpansFn, wantV0 ServiceNameAssertions) func(t *testing.T) {
	wrap := func(t *testing.T, serviceOverride string) []*v2mock.Span {
		spans := genSpans(t, serviceOverride)
		ss := make([]*v2mock.Span, len(spans))
		for i, s := range spans {
			ss[i] = s.(mocktracer.MockspanV2Adapter).Span
		}
		return ss
	}
	// TODO: fix return namingschematest.NewServiceNameTest(wrap, namingschematest.ServiceNameAssertions(wantV0))
	return func(t *testing.T) {}
}

// AssertSpansFn allows to make assertions on the generated spans.
type AssertSpansFn func(t *testing.T, spans []mocktracer.Span)

// NewSpanNameTest returns a new test that runs the provided assertion functions for each schema version.
func NewSpanNameTest(genSpans GenSpansFn, assertV0 AssertSpansFn, assertV1 AssertSpansFn) func(t *testing.T) {
	gsWrap := func(t *testing.T, serviceOverride string) []*v2mock.Span {
		spans := genSpans(t, serviceOverride)
		ss := make([]*v2mock.Span, len(spans))
		for i, s := range spans {
			ss[i] = s.(mocktracer.MockspanV2Adapter).Span
		}
		return ss
	}
	aV0Wrap := func(t *testing.T, spans []*v2mock.Span) {
		ss := make([]mocktracer.Span, len(spans))
		for i, s := range spans {
			ss[i] = mocktracer.MockspanV2Adapter{Span: s}
		}
		assertV0(t, ss)
	}
	aV1Wrap := func(t *testing.T, spans []*v2mock.Span) {
		ss := make([]mocktracer.Span, len(spans))
		for i, s := range spans {
			ss[i] = mocktracer.MockspanV2Adapter{Span: s}
		}
		assertV1(t, ss)
	}
	// TODO: fix return namingschematest.NewSpanNameTest(gsWrap, aV0Wrap, aV1Wrap)
	return func(t *testing.T) {}
}
