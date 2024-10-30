// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"database/sql/driver"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
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
	dbStats            bool
	statsdClient       internal.StatsdClient
}

// checkStatsdRequired adds a statsdclient onto the config if dbstats is enabled
// NOTE: For now, the only use-case for a statsdclient is the dbStats feature. If a statsdclient becomes necessary for other items in future work, then this logic should change
func (c *config) checkStatsdRequired() {
	if c.dbStats && c.statsdClient == nil {
		// contrib/database/sql's statsdclient should always inherit its address from the tracer's statsdclient via the globalconfig
		// destination is not user-configurable
		sc, err := internal.NewStatsdClient(globalconfig.DogstatsdAddr(), statsTags(c))
		if err == nil {
			c.statsdClient = sc
			log.Debug("Metrics from the database/sql contrib will be sent to %v", globalconfig.DogstatsdAddr())
		} else {
			log.Warn("Error creating statsd client for database/sql contrib; DB Stats disabled: %v", err)
			c.dbStats = false
		}
	}
}

func (c *config) checkDBMPropagation(driverName string, driver driver.Driver, dsn string) {
	if c.dbmPropagationMode == tracer.DBMPropagationModeFull {
		if dsn == "" {
			dsn = c.dsn
		}
		if dbSystem, ok := dbmFullModeUnsupported(driverName, driver, dsn); ok {
			log.Warn("Using DBM_PROPAGATION_MODE in 'full' mode is not supported for %s, downgrading to 'service' mode. "+
				"See https://docs.datadoghq.com/database_monitoring/connect_dbm_and_apm/ for more info.",
				dbSystem,
			)
			c.dbmPropagationMode = tracer.DBMPropagationModeService
		}
	}
}

func dbmFullModeUnsupported(driverName string, driver driver.Driver, dsn string) (string, bool) {
	const (
		sqlServer = "SQL Server"
		oracle    = "Oracle"
	)
	// check if the driver package path is one of the unsupported ones.
	if tp := reflect.TypeOf(driver); tp != nil && (tp.Kind() == reflect.Pointer || tp.Kind() == reflect.Struct) {
		pkgPath := ""
		switch tp.Kind() {
		case reflect.Pointer:
			pkgPath = tp.Elem().PkgPath()
		case reflect.Struct:
			pkgPath = tp.PkgPath()
		}
		driverPkgs := [][3]string{
			{"github.com", "denisenkom/go-mssqldb", sqlServer},
			{"github.com", "microsoft/go-mssqldb", sqlServer},
			{"github.com", "sijms/go-ora", oracle},
		}
		for _, dp := range driverPkgs {
			prefix, pkgName, dbSystem := dp[0], dp[1], dp[2]

			// compare without the prefix to make it work for vendoring.
			// also, compare only the prefix to make the comparison work when using major versions
			// of the libraries or subpackages.
			if strings.HasPrefix(strings.TrimPrefix(pkgPath, prefix+"/"), pkgName) {
				return dbSystem, true
			}
		}
	}

	// check the DSN if provided.
	if dsn != "" {
		prefixes := [][2]string{
			{"oracle://", oracle},
			{"sqlserver://", sqlServer},
		}
		for _, pr := range prefixes {
			prefix, dbSystem := pr[0], pr[1]
			if strings.HasPrefix(dsn, prefix) {
				return dbSystem, true
			}
		}
	}

	// lastly, check if the registered driver name is one of the unsupported ones.
	driverNames := [][2]string{
		{"sqlserver", sqlServer},
		{"mssql", sqlServer},
		{"azuresql", sqlServer},
		{"oracle", oracle},
	}
	for _, dn := range driverNames {
		name, dbSystem := dn[0], dn[1]
		if name == driverName {
			return dbSystem, true
		}
	}
	return "", false
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
		cfg.dbStats = rc.dbStats
	}
}

func getServiceName(driverName string, rc *registerConfig) string {
	defaultServiceName := fmt.Sprintf("%s.db", driverName)
	if rc != nil {
		// if service name was set during Register, we use that value as default instead of
		// the one calculated above.
		defaultServiceName = rc.serviceName
	}
	return namingschema.ServiceNameOverrideV0(defaultServiceName, defaultServiceName)
}

func getSpanName(driverName string) string {
	dbSystem := driverName
	if normalizedDBSystem, ok := normalizeDBSystem(driverName); ok {
		dbSystem = normalizedDBSystem
	}
	return namingschema.DBOpName(dbSystem, fmt.Sprintf("%s.query", driverName))
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

// WithDBStats enables polling of DBStats metrics
// ref: https://pkg.go.dev/database/sql#DBStats
// These metrics are submitted to Datadog and are not billed as custom metrics
func WithDBStats() Option {
	return func(cfg *config) {
		cfg.dbStats = true
	}
}
