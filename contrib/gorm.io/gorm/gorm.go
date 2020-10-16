// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

// Package gorm provides helper functions for tracing the gorm.io/gorm package (https://github.com/go-gorm/gorm).
package gorm

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
	gormConfigKey        = "dd-trace-go:config"
	gormSpanStartTimeKey = "dd-trace-go:span"
)

// Open opens a new (traced) database connection. The used driver must be formerly registered
// using (gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql).Register.
func Open(getDialector func(db *sql.DB) gorm.Dialector, driverName, source string, opts ...Option) (*gorm.DB, error) {
	sqldb, err := sqltraced.Open(driverName, source)
	if err != nil {
		return nil, err
	}

	dialector := getDialector(sqldb)
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
		return func(db *gorm.DB) {
			after(db, operationName)
		}
	}

	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}

	ctx := context.Background()
	if db.Statement != nil && db.Statement.Context != nil {
		ctx = db.Statement.Context
	}

	db = db.WithContext(context.WithValue(ctx, gormConfigKey, cfg))

	cb := db.Callback()
	err := cb.Create().Before("gorm:create").Register("dd-trace-go:before_create", before)
	if err != nil {
		return db, err
	}
	err = cb.Create().After("gorm:create").Register("dd-trace-go:after_create", afterFunc("gorm.create"))
	if err != nil {
		return db, err
	}
	err = cb.Update().Before("gorm:update").Register("dd-trace-go:before_update", before)
	if err != nil {
		return db, err
	}
	err = cb.Update().After("gorm:update").Register("dd-trace-go:after_update", afterFunc("gorm.update"))
	if err != nil {
		return db, err
	}
	err = cb.Delete().Before("gorm:delete").Register("dd-trace-go:before_delete", before)
	if err != nil {
		return db, err
	}
	err = cb.Delete().After("gorm:delete").Register("dd-trace-go:after_delete", afterFunc("gorm.delete"))
	if err != nil {
		return db, err
	}
	err = cb.Query().Before("gorm:query").Register("dd-trace-go:before_query", before)
	if err != nil {
		return db, err
	}
	err = cb.Query().After("gorm:query").Register("dd-trace-go:after_query", afterFunc("gorm.query"))
	if err != nil {
		return db, err
	}
	err = cb.Row().Before("gorm:query").Register("dd-trace-go:before_row_query", before)
	if err != nil {
		return db, err
	}
	err = cb.Row().After("gorm:query").Register("dd-trace-go:after_row_query", afterFunc("gorm.row_query"))
	if err != nil {
		return db, err
	}

	return db, nil
}

// WithContext attaches the specified context to the given db. The context will
// be used as a basis for creating new spans. An example use case is providing
// a context which contains a span to be used as a parent.
func WithContext(ctx context.Context, db *gorm.DB) *gorm.DB {
	if ctx == nil {
		return db
	}
	if db.Statement != nil && db.Statement.Context != nil {
		ctx = context.WithValue(ctx, gormConfigKey, db.Statement.Context.Value(gormConfigKey))
	}

	return db.WithContext(ctx)
}

// ContextFromDB returns any context previously attached to db using WithContext,
// otherwise returning context.Background.
func ContextFromDB(db *gorm.DB) context.Context {
	if db.Statement != nil {
		if v, ok := db.Statement.Context.(context.Context); ok {
			return v
		}
	}

	return context.Background()
}

func before(scope *gorm.DB) {
	if scope.Statement != nil && scope.Statement.Context != nil {
		scope.Statement.Context = context.WithValue(scope.Statement.Context, gormSpanStartTimeKey, time.Now())
	}
}

func after(db *gorm.DB, operationName string) {
	ctx := db.Statement.Context
	if ctx == nil {
		return
	}

	cfg, ok := ctx.Value(gormConfigKey).(*config)
	if !ok {
		return
	}

	t, ok := ctx.Value(gormSpanStartTimeKey).(time.Time)
	if !ok {
		return
	}

	opts := []ddtrace.StartSpanOption{
		tracer.StartTime(t),
		tracer.ServiceName(cfg.serviceName),
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.ResourceName(db.Statement.SQL.String()),
	}
	if !math.IsNaN(cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}

	span, _ := tracer.StartSpanFromContext(ctx, operationName, opts...)
	span.Finish(tracer.WithError(db.Error))
}
