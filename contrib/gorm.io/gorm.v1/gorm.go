// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package gorm provides helper functions for tracing the gorm.io/gorm package (https://github.com/go-gorm/gorm).
package gorm

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"gorm.io/gorm"
)

const componentName = "gorm.io/gorm.v1"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported(componentName)
}

type key string

const (
	gormSpanStartTimeKey = key("dd-trace-go:span")
)

type tracePlugin struct {
	options []Option
}

// NewTracePlugin returns a new gorm.Plugin that enhances the underlying *gorm.DB with tracing.
func NewTracePlugin(opts ...Option) gorm.Plugin {
	return tracePlugin{
		options: opts,
	}
}

func (tracePlugin) Name() string {
	return "DDTracePlugin"
}

func (g tracePlugin) Initialize(db *gorm.DB) error {
	_, err := withCallbacks(db, g.options...)
	return err
}

// Open opens a new (traced) database connection. The used driver must be formerly registered
// using (gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql).Register.
func Open(dialector gorm.Dialector, cfg *gorm.Config, opts ...Option) (*gorm.DB, error) {
	var db *gorm.DB
	var err error
	if cfg != nil {
		db, err = gorm.Open(dialector, cfg)
	} else {
		db, err = gorm.Open(dialector)
	}
	if err != nil {
		return db, err
	}
	return withCallbacks(db, opts...)
}

func withCallbacks(db *gorm.DB, opts ...Option) (*gorm.DB, error) {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("Registering Callbacks: %#v", cfg)

	afterFunc := func() func(*gorm.DB) {
		return func(db *gorm.DB) {
			after(db, cfg)
		}
	}

	beforeFunc := func(operationName string) func(*gorm.DB) {
		return func(db *gorm.DB) {
			before(db, operationName, cfg)
		}
	}

	cb := db.Callback()
	err := cb.Create().Before("gorm:create").Register("dd-trace-go:before_create", beforeFunc("gorm.create"))
	if err != nil {
		return db, err
	}
	err = cb.Create().After("gorm:create").Register("dd-trace-go:after_create", afterFunc())
	if err != nil {
		return db, err
	}
	err = cb.Update().Before("gorm:update").Register("dd-trace-go:before_update", beforeFunc("gorm.update"))
	if err != nil {
		return db, err
	}
	err = cb.Update().After("gorm:update").Register("dd-trace-go:after_update", afterFunc())
	if err != nil {
		return db, err
	}
	err = cb.Delete().Before("gorm:delete").Register("dd-trace-go:before_delete", beforeFunc("gorm.delete"))
	if err != nil {
		return db, err
	}
	err = cb.Delete().After("gorm:delete").Register("dd-trace-go:after_delete", afterFunc())
	if err != nil {
		return db, err
	}
	err = cb.Query().Before("gorm:query").Register("dd-trace-go:before_query", beforeFunc("gorm.query"))
	if err != nil {
		return db, err
	}
	err = cb.Query().After("gorm:query").Register("dd-trace-go:after_query", afterFunc())
	if err != nil {
		return db, err
	}
	err = cb.Row().Before("gorm:row").Register("dd-trace-go:before_row_query", beforeFunc("gorm.row_query"))
	if err != nil {
		return db, err
	}
	err = cb.Row().After("gorm:row").Register("dd-trace-go:after_row_query", afterFunc())
	if err != nil {
		return db, err
	}
	err = cb.Raw().Before("gorm:raw").Register("dd-trace-go:before_raw_query", beforeFunc("gorm.raw_query"))
	if err != nil {
		return db, err
	}
	err = cb.Raw().After("gorm:raw").Register("dd-trace-go:after_raw_query", afterFunc())
	if err != nil {
		return db, err
	}
	return db, nil
}

func before(db *gorm.DB, operationName string, cfg *config) {
	if db.Statement == nil || db.Statement.Context == nil {
		return
	}
	if db.Config == nil || db.Config.DryRun {
		return
	}
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(cfg.serviceName),
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.Tag(ext.Component, componentName),
	}
	if !math.IsNaN(cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	for key, tagFn := range cfg.tagFns {
		if tagFn != nil {
			opts = append(opts, tracer.Tag(key, tagFn(db)))
		}
	}

	_, ctx := tracer.StartSpanFromContext(db.Statement.Context, operationName, opts...)
	db.Statement.Context = ctx
}

func after(db *gorm.DB, cfg *config) {
	if db.Statement == nil || db.Statement.Context == nil {
		return
	}
	if db.Config == nil || db.Config.DryRun {
		return
	}
	span, ok := tracer.SpanFromContext(db.Statement.Context)
	if ok {
		var dbErr error
		if cfg.errCheck(db.Error) {
			dbErr = db.Error
		}
		span.SetTag(ext.ResourceName, db.Statement.SQL.String())
		span.Finish(tracer.WithError(dbErr))
	}
}
