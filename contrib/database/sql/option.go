// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"fmt"
	"math"
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

type config struct {
	serviceName        string
	spanName           string
	analyticsRate      float64
	dsn                string
	ignoreQueryTypes   map[QueryType]struct{}
	childSpansOnly     bool
	errCheck           func(err error) bool
	tags               map[string]interface{}
	dbmPropagationMode tracer.DBMPropagationMode
}

// Option represents an option that can be passed to Register, Open or OpenDB.
type Option func(*config)

type registerConfig = config

// RegisterOption has been deprecated in favor of Option.
type RegisterOption = Option

func defaults(cfg *config, driverName string, rc *registerConfig) {
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_SQL_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
	mode := os.Getenv("DD_DBM_PROPAGATION_MODE")
	if mode == "" {
		mode = os.Getenv("DD_TRACE_SQL_COMMENT_INJECTION_MODE")
	}
	cfg.dbmPropagationMode = tracer.DBMPropagationMode(mode)
	cfg.serviceName = getServiceName(driverName, rc)
	cfg.spanName = getSpanName(driverName)
	if rc != nil {
		// use registered config as the default value for some options
		if math.IsNaN(cfg.analyticsRate) {
			cfg.analyticsRate = rc.analyticsRate
		}
		if cfg.dbmPropagationMode == tracer.DBMPropagationModeUndefined {
			cfg.dbmPropagationMode = rc.dbmPropagationMode
		}
		cfg.errCheck = rc.errCheck
		cfg.ignoreQueryTypes = rc.ignoreQueryTypes
		cfg.childSpansOnly = rc.childSpansOnly
	}
}

func getServiceName(driverName string, rc *registerConfig) string {
	defaultServiceName := fmt.Sprintf("%s.db", driverName)
	if rc != nil {
		// if service name was set during Register, we use that value as default instead of
		// the one calculated above.
		defaultServiceName = rc.serviceName
	}
	return namingschema.NewDefaultServiceName(
		defaultServiceName,
		namingschema.WithOverrideV0(defaultServiceName),
	).GetName()
}

func getSpanName(driverName string) string {
	dbSystem := driverName
	if normalizedDBSystem, ok := normalizeDBSystem(driverName); ok {
		dbSystem = normalizedDBSystem
	}
	return namingschema.NewDBOutboundOp(
		dbSystem,
		namingschema.WithOverrideV0(fmt.Sprintf("%s.query", driverName)),
	).GetName()
}

// WithServiceName sets the given service name when registering a driver,
// or opening a database connection.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(cfg *config) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithDSN allows the data source name (DSN) to be provided when
// using OpenDB and a driver.Connector.
// The value is used to automatically set tags on spans.
func WithDSN(name string) Option {
	return func(cfg *config) {
		cfg.dsn = name
	}
}

// WithIgnoreQueryTypes specifies the query types for which spans should not be
// created.
func WithIgnoreQueryTypes(qtypes ...QueryType) Option {
	return func(cfg *config) {
		if cfg.ignoreQueryTypes == nil {
			cfg.ignoreQueryTypes = make(map[QueryType]struct{})
		}
		for _, qt := range qtypes {
			cfg.ignoreQueryTypes[qt] = struct{}{}
		}
	}
}

// WithChildSpansOnly causes spans to be created only when
// there is an existing parent span in the Context.
func WithChildSpansOnly() Option {
	return func(cfg *config) {
		cfg.childSpansOnly = true
	}
}

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever a database/sql operation
// finishes with an error
func WithErrorCheck(fn func(err error) bool) Option {
	return func(cfg *config) {
		cfg.errCheck = fn
	}
}

// WithCustomTag will attach the value to the span tagged by the key
func WithCustomTag(key string, value interface{}) Option {
	return func(cfg *config) {
		if cfg.tags == nil {
			cfg.tags = make(map[string]interface{})
		}
		cfg.tags[key] = value
	}
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
	return func(cfg *config) {
		cfg.dbmPropagationMode = mode
	}
}
