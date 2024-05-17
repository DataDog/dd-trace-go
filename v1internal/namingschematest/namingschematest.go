// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// Package namingschematest provides support for automated testing of Go packages using the v1 API.
// Note that this package is for dd-trace-go.v1 internal testing utilities only.
// This package is not intended for use by external consumers, no API stability is guaranteed.
package namingschematest

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/contrib/namingschematest"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
)

// GenSpansFn is used across different functions from this package to generate spans. It should be implemented in the
// tests that use this package.
// The provided serviceOverride string should be used to set the specific integration's WithService option (if
// available) when initializing and configuring the package.
// This type is not intended for use by external consumers, no API stability is guaranteed.
type GenSpansFn = namingschematest.GenSpansFn

// ServiceNameAssertions contains assertions for different test cases used inside the generated test
// from NewServiceNameTest.
// []string fields in this struct represent the assertions to be made against the returned []mocktracer.Span from
// GenSpansFn in the same order.
// This type is not intended for use by external consumers, no API stability is guaranteed.
type ServiceNameAssertions = namingschematest.ServiceNameAssertions

// NewServiceNameTest generates a new test for span service names using the naming schema versioning.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func NewServiceNameTest(genSpans GenSpansFn, wantV0 ServiceNameAssertions) func(t *testing.T) {
	return namingschematest.NewServiceNameTest(genSpans, wantV0)
}

// AssertSpansFn allows to make assertions on the generated spans.
// This type is not intended for use by external consumers, no API stability is guaranteed.
type AssertSpansFn = namingschematest.AssertSpansFn

// NewSpanNameTest returns a new test that runs the provided assertion functions for each schema version.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func NewSpanNameTest(genSpans GenSpansFn, assertV0 AssertSpansFn, assertV1 AssertSpansFn) func(t *testing.T) {
	return namingschematest.NewSpanNameTest(genSpans, assertV0, assertV1)
}

// NewKafkaTest creates a new test for Kafka naming schema.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func NewKafkaTest(genSpans GenSpansFn) func(t *testing.T) {
	return namingschematest.NewKafkaTest(genSpans)
}

// Option is a type used to customize behavior of functions in this package.
// This type is not intended for use by external consumers, no API stability is guaranteed.
type Option = namingschematest.Option

// NewHTTPServerTest creates a new test for HTTP server naming schema.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func NewHTTPServerTest(genSpans GenSpansFn, defaultName string, opts ...Option) func(t *testing.T) {
	return namingschematest.NewHTTPServerTest(genSpans, defaultName, opts...)
}

// Version is a type used to represent the different versions of the naming schema.
// This type is not intended for use by external consumers, no API stability is guaranteed.
type Version = namingschema.Version

// WithServiceNameAssertions allows you to override the service name assertions for a specific naming schema version.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func WithServiceNameAssertions(v Version, s ServiceNameAssertions) Option {
	return namingschematest.WithServiceNameAssertions(v, s)
}

// NewMongoDBTest creates a new test for MongoDB naming schema.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func NewMongoDBTest(genSpans GenSpansFn, defaultServiceName string) func(t *testing.T) {
	return namingschematest.NewMongoDBTest(genSpans, defaultServiceName)
}

// NewRedisTest creates a new test for Redis naming schema.
// This function is not intended for use by external consumers, no API stability is guaranteed.
func NewRedisTest(genSpans GenSpansFn, defaultServiceName string) func(t *testing.T) {
	return namingschematest.NewRedisTest(genSpans, defaultServiceName)
}
