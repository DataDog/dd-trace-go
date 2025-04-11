// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gqlgen

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/99designs/gqlgen/v2"
)

const defaultServiceName = "graphql"

// An Option configures the gqlgen integration.
type Option = v2.Option

// WithAnalytics enables or disables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return v2.WithAnalytics(on)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return v2.WithAnalyticsRate(rate)
}

// WithServiceName sets the given service name for the gqlgen server.
func WithServiceName(name string) Option {
	return v2.WithService(name)
}

// WithoutTraceIntrospectionQuery skips creating spans for fields when the operation name is IntrospectionQuery.
func WithoutTraceIntrospectionQuery() Option {
	return v2.WithoutTraceIntrospectionQuery()
}

// WithoutTraceTrivialResolvedFields skips creating spans for fields that have a trivial resolver.
// For example, a field resolved from an object w/o requiring a custom method is considered trivial.
func WithoutTraceTrivialResolvedFields() Option {
	return v2.WithoutTraceTrivialResolvedFields()
}

// WithCustomTag will attach the value to the span tagged by the key.
func WithCustomTag(key string, value interface{}) Option {
	return v2.WithCustomTag(key, value)
}

// WithErrorExtensions allows to configure the error extensions to include in the error span events.
func WithErrorExtensions(errExtensions ...string) Option {
	return v2.WithErrorExtensions(errExtensions...)
}
