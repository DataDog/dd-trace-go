// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

// Package gorm provides helper functions for tracing the jinzhu/gorm package (https://github.com/jinzhu/gorm).
package gorm

import (
	"context"
	"math"

	sqltraced "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/jinzhu/gorm"
)

const (
	gormContextKey = "dd-trace-go:context"
	gormConfigKey  = "dd-trace-go:config"
)

// Open opens a new (traced) database connection. The used dialect must be formerly registered
// using (gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql).Register.
func Open(dialect, source string, opts ...Option) (*gorm.DB, error) {
	sqldb, err := sqltraced.Open(dialect, source)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(dialect, sqldb)
	if err != nil {
		return db, err
	}
	return WithCallbacks(db, opts...), err
}

// WithCallbacks registers callbacks to the gorm.DB for tracing.
func WithCallbacks(db *gorm.DB, opts ...Option) *gorm.DB {
	cb := db.Callback()
	cb.Create().Before("dd-trace-go").Register("dd-trace-go:before_create", beforeFunc("gorm.create"))
	cb.Create().After("dd-trace-go").Register("dd-trace-go:after_create", after)
	cb.Update().Before("dd-trace-go").Register("dd-trace-go:before_update", beforeFunc("gorm.update"))
	cb.Update().After("dd-trace-go").Register("dd-trace-go:after_update", after)
	cb.Delete().Before("dd-trace-go").Register("dd-trace-go:before_delete", beforeFunc("gorm.delete"))
	cb.Delete().After("dd-trace-go").Register("dd-trace-go:after_delete", after)
	cb.Query().Before("dd-trace-go").Register("dd-trace-go:before_query", beforeFunc("gorm.query"))
	cb.Query().After("dd-trace-go").Register("dd-trace-go:after_query", after)
	cb.RowQuery().Before("dd-trace-go").Register("dd-trace-go:before_row_query", beforeFunc("gorm.row_query"))
	cb.RowQuery().After("dd-trace-go").Register("dd-trace-go:after_row_query", after)

	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	return db.Set(gormConfigKey, cfg)
}

// WithContext returns a new gorm.DB with the context added
// to its settings store.
func WithContext(ctx context.Context, db *gorm.DB) *gorm.DB {
	if ctx == nil {
		return db
	}
	db = db.Set(gormContextKey, ctx)
	return db
}

func beforeFunc(operationName string) func(*gorm.Scope) {
	return func(scope *gorm.Scope) {
		before(scope, operationName)
	}
}

func before(scope *gorm.Scope, operationName string) {
	v, ok := scope.Get(gormContextKey)
	if !ok {
		return
	}
	ctx := v.(context.Context)

	c, ok := scope.Get(gormConfigKey)
	if !ok {
		return
	}
	cfg := c.(*config)

	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(cfg.serviceName),
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.ResourceName(scope.SQL),
	}
	if !math.IsNaN(cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	_, ctx = tracer.StartSpanFromContext(ctx, operationName, opts...)
	scope.Set(gormContextKey, ctx)
}

func after(scope *gorm.Scope) {
	v, ok := scope.Get(gormContextKey)
	if !ok {
		return
	}
	ctx := v.(context.Context)

	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}

	span.Finish(tracer.WithError(scope.DB().Error))
}
