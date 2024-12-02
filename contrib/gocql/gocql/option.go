// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gocql

import (
	"math"
	"os"

	"golang.org/x/mod/semver"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

const defaultServiceName = "gocql.query"

type config struct {
	serviceName, resourceName            string
	querySpanName, batchSpanName         string
	noDebugStack                         bool
	analyticsRate                        float64
	errCheck                             func(err error) bool
	customTags                           map[string]interface{}
	clusterTagLegacyMode                 bool
	traceQuery, traceBatch, traceConnect bool
}

// WrapOption represents an option that can be passed to WrapQuery.
type WrapOption func(*config)

func defaultConfig() *config {
	cfg := &config{
		traceQuery:   true,
		traceBatch:   true,
		traceConnect: true,
	}
	cfg.serviceName = namingschema.ServiceNameOverrideV0(defaultServiceName, defaultServiceName)
	cfg.querySpanName = namingschema.OpName(namingschema.CassandraOutbound)
	cfg.batchSpanName = namingschema.OpNameOverrideV0(namingschema.CassandraOutbound, "cassandra.batch")
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_GOCQL_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
	if compatMode := os.Getenv("DD_TRACE_GOCQL_COMPAT"); compatMode != "" {
		if semver.IsValid(compatMode) {
			cfg.clusterTagLegacyMode = semver.Compare(semver.MajorMinor(compatMode), "v1.65") <= 0
		} else {
			log.Warn("ignoring DD_TRACE_GOCQL_COMPAT: invalid version %q", compatMode)
		}
	}
	cfg.errCheck = func(error) bool { return true }
	return cfg
}

// WithServiceName sets the given service name for the returned query.
func WithServiceName(name string) WrapOption {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithResourceName sets a custom resource name to be used with the traced query.
// By default, the query statement is extracted automatically. This method should
// be used when a different resource name is desired or in performance critical
// environments. The gocql library returns the query statement using an fmt.Sprintf
// call, which can be costly when called repeatedly. Using WithResourceName will
// avoid that call. Under normal circumstances, it is safe to rely on the default.
func WithResourceName(name string) WrapOption {
	return func(cfg *config) {
		cfg.resourceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) WrapOption {
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
func WithAnalyticsRate(rate float64) WrapOption {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() WrapOption {
	return func(cfg *config) {
		cfg.noDebugStack = true
	}
}

func (c *config) shouldIgnoreError(err error) bool {
	return c != nil && c.errCheck != nil && !c.errCheck(err)
}

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever a CQL request
// finishes with an error.
func WithErrorCheck(fn func(err error) bool) WrapOption {
	return func(cfg *config) {
		// When the error is explicitly marked as not-an-error, that is
		// when this errCheck function returns false, the APM code will
		// just skip the error and pretend the span was successful.
		//
		// A typical use-case is gocql.ErrNotFound which is returned when scanning data,
		// but no data is available so zero rows are returned.
		//
		// This only affects whether the span/trace is marked as success/error,
		// the calls to the gocql API still return the upstream error code.
		cfg.errCheck = fn
	}
}

// WithCustomTag will attach the value to the span tagged by the key.
func WithCustomTag(key string, value interface{}) WrapOption {
	return func(cfg *config) {
		if cfg.customTags == nil {
			cfg.customTags = make(map[string]interface{})
		}
		cfg.customTags[key] = value
	}
}

// WithTraceQuery will enable tracing for queries (default is true).
// This option only takes effect in CreateTracedSession and NewObserver.
func WithTraceQuery(enabled bool) WrapOption {
	return func(cfg *config) {
		cfg.traceQuery = enabled
	}
}

// WithTraceBatch will enable tracing for batches (default is true).
// This option only takes effect in CreateTracedSession and NewObserver.
func WithTraceBatch(enabled bool) WrapOption {
	return func(cfg *config) {
		cfg.traceBatch = enabled
	}
}

// WithTraceConnect will enable tracing for connections (default is true).
// This option only takes effect in CreateTracedSession and NewObserver.
func WithTraceConnect(enabled bool) WrapOption {
	return func(cfg *config) {
		cfg.traceConnect = enabled
	}
}
