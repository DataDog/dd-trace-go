// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	v2 "github.com/DataDog/dd-trace-go/v2/contrib/database/sql"
	v2tracer "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// Option represents an option that can be passed to Register, Open or OpenDB.
type Option = v2.Option

// RegisterOption has been deprecated in favor of Option.
type RegisterOption = Option

// WithServiceName sets the given service name when registering a driver,
// or opening a database connection.
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

// WithDSN allows the data source name (DSN) to be provided when
// using OpenDB and a driver.Connector.
// The value is used to automatically set tags on spans.
func WithDSN(name string) Option {
	return v2.WithDSN(name)
}

// WithIgnoreQueryTypes specifies the query types for which spans should not be
// created.
func WithIgnoreQueryTypes(qtypes ...QueryType) Option {
	return v2.WithIgnoreQueryTypes(qtypes...)
}

// WithChildSpansOnly causes spans to be created only when
// there is an existing parent span in the Context.
func WithChildSpansOnly() Option {
	return v2.WithChildSpansOnly()
}

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever a database/sql operation
// finishes with an error
func WithErrorCheck(fn func(err error) bool) Option {
	return v2.WithErrorCheck(fn)
}

// WithCustomTag will attach the value to the span tagged by the key
func WithCustomTag(key string, value interface{}) Option {
	return v2.WithCustomTag(key, value)
}

// WithSQLCommentInjection enables injection of tags as sql comments on traced queries.
// This includes dynamic values like span id, trace id and sampling priority which can make queries
// unique for some cache implementations.
//
// Deprecated: Use WithDBMPropagation instead.
func WithSQLCommentInjection(mode tracer.SQLCommentInjectionMode) Option {
	return WithDBMPropagation(tracer.DBMPropagationMode(mode))
}

// WithDBMPropagation enables injection of tags as sql comments on traced queries.
// This includes dynamic values like span id, trace id and the sampled flag which can make queries
// unique for some cache implementations. Use DBMPropagationModeService if this is a concern.
//
// Note that enabling sql comment propagation results in potentially confidential data (service names)
// being stored in the databases which can then be accessed by other 3rd parties that have been granted
// access to the database.
func WithDBMPropagation(mode tracer.DBMPropagationMode) Option {
	return v2.WithDBMPropagation(v2tracer.DBMPropagationMode(mode))
}

// WithDBStats enables polling of DBStats metrics
// ref: https://pkg.go.dev/database/sql#DBStats
// These metrics are submitted to Datadog and are not billed as custom metrics
func WithDBStats() Option {
	return v2.WithDBStats()
}
