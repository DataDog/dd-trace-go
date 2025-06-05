// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package graphql

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/graph-gophers/graphql-go/v2"
)

// Option represents an option that can be used customize the Tracer.
type Option = v2.Option

// WithServiceName sets the given service name for the client.
func WithServiceName(name string) Option {
	return v2.WithService(name)
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return v2.WithAnalytics(on)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return v2.WithAnalyticsRate(rate)
}

// WithOmitTrivial enables omission of graphql fields marked as trivial. This
// also opts trivial fields out of Threat Detection (and blocking).
func WithOmitTrivial() Option {
	return v2.WithOmitTrivial()
}

// WithTraceVariables enables tracing of variables passed into GraphQL queries
// and resolvers.
func WithTraceVariables() Option {
	return v2.WithTraceVariables()
}

// WithErrorExtensions allows to configure the error extensions to include in the error span events.
func WithErrorExtensions(errExtensions ...string) Option {
	return v2.WithErrorExtensions(errExtensions...)
}
