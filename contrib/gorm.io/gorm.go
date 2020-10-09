// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Package gorm provides helper functions for tracing the gorm.io/gorm package (https://github.com/go-gorm/gorm).
package gormv2

import (
	"context"
	"database/sql"
	"math"
	"time"

	sqltraced "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"gorm.io/gorm"
)

const (
	gormContextKey       = "dd-trace-go:context"
	gormConfigKey        = "dd-trace-go:config"
	gormSpanStartTimeKey = "dd-trace-go:span"
)

// Open opens a new (traced) database connection. The used dialect must be formerly registered
// using (gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql).Register.
func Open(getDialector func(db *sql.DB) gorm.Dialector, source string, opts ...Option) (*gorm.DB, error) {
	dialector := getDialector(nil)

	sqldb, err := sqltraced.Open(dialector.Name(), source)
	if err != nil {
		return nil, err
	}

	dialector = getDialector(sqldb)

	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return db, err
	}

	return WithCallbacks(db, opts...)
}

// WithCallbacks registers callbacks to the gorm.DB for tracing.
// It should be called once, after opening the db.
// The callbacks are triggered by Create, Update, Delete,
// Query and RowQuery operations.
func WithCallbacks(db *gorm.DB, opts ...Option) (*gorm.DB, error) {
	afterFunc := func(operationName string) func(*gorm.DB) {
		return func(scope *gorm.DB) {
			after(scope, operationName)
		}
	}

	cb := db.Callback()
	err := cb.Create().Before("gorm:before_create").Register("dd-trace-go:before_create", before)
	if err != nil {
		return db, err
	}
	err = cb.Create().After("gorm:after_create").Register("dd-trace-go:after_create", afterFunc("gorm.create"))
	if err != nil {
		return db, err
	}
	err = cb.Update().Before("gorm:before_update").Register("dd-trace-go:before_update", before)
	if err != nil {
		return db, err
	}
	err = cb.Update().After("gorm:after_update").Register("dd-trace-go:after_update", afterFunc("gorm.update"))
	if err != nil {
		return db, err
	}
	err = cb.Delete().Before("gorm:before_delete").Register("dd-trace-go:before_delete", before)
	if err != nil {
		return db, err
	}
	err = cb.Delete().After("gorm:after_delete").Register("dd-trace-go:after_delete", afterFunc("gorm.delete"))
	if err != nil {
		return db, err
	}
	err = cb.Query().Before("gorm:query").Register("dd-trace-go:before_query", before)
	if err != nil {
		return db, err
	}
	err = cb.Query().After("gorm:after_query").Register("dd-trace-go:after_query", afterFunc("gorm.query"))
	if err != nil {
		return db, err
	}
	err = cb.Row().Before("gorm:row_query").Register("dd-trace-go:before_row_query", before)
	if err != nil {
		return db, err
	}
	err = cb.Row().After("gorm:row_query").Register("dd-trace-go:after_row_query", afterFunc("gorm.row_query"))
	if err != nil {
		return db, err
	}

	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}

	return db.Set(gormConfigKey, cfg), nil
}

// WithContext attaches the specified context to the given db. The context will
// be used as a basis for creating new spans. An example use case is providing
// a context which contains a span to be used as a parent.
func WithContext(ctx context.Context, db *gorm.DB) *gorm.DB {
	if ctx == nil {
		return db
	}
	db = db.Set(gormContextKey, ctx)
	return db
}

// ContextFromDB returns any context previously attached to db using WithContext,
// otherwise returning context.Background.
func ContextFromDB(db *gorm.DB) context.Context {
	if v, ok := db.Get(gormContextKey); ok {
		if ctx, ok := v.(context.Context); ok {
			return ctx
		}
	}
	return context.Background()
}

func before(scope *gorm.DB) {
	scope.Set(gormSpanStartTimeKey, time.Now())
}

func after(scope *gorm.DB, operationName string) {
	v, ok := scope.Get(gormContextKey)
	if !ok {
		return
	}
	ctx := v.(context.Context)

	v, ok = scope.Get(gormConfigKey)
	if !ok {
		return
	}
	cfg := v.(*config)

	v, ok = scope.Get(gormSpanStartTimeKey)
	if !ok {
		return
	}
	t, ok := v.(time.Time)

	opts := []ddtrace.StartSpanOption{
		tracer.StartTime(t),
		tracer.ServiceName(cfg.serviceName),
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.ResourceName(scope.Statement.SQL.String()),
	}
	if !math.IsNaN(cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}

	span, _ := tracer.StartSpanFromContext(ctx, operationName, opts...)
	span.Finish(tracer.WithError(scope.Error))
}
