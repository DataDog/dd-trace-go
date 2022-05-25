// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package gorm provides helper functions for tracing the gorm.io/gorm package (https://github.com/go-gorm/gorm).
package gorm

import (
	"context"
	"math"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"gorm.io/gorm"
)

type key string

const (
	gormSpanStartTimeKey = key("dd-trace-go:span")
)

// Open opens a new (traced) database connection. The used driver must be formerly registered
// using (gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql).Register.
func Open(dialector gorm.Dialector, cfg *gorm.Config, opts ...Option) (*gorm.DB, error) {
	db, err := gorm.Open(dialector, cfg)
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

	afterFunc := func(operationName string) func(*gorm.DB) {
		return func(db *gorm.DB) {
			after(db, operationName, cfg)
		}
	}

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

func before(scope *gorm.DB) {
	if scope.Statement != nil && scope.Statement.Context != nil {
		scope.Statement.Context = context.WithValue(scope.Statement.Context, gormSpanStartTimeKey, time.Now())
	}
}

func after(db *gorm.DB, operationName string, cfg *config) {
	if db.Statement == nil || db.Statement.Context == nil {
		return
	}

	ctx := db.Statement.Context
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

	if cfg.tagFns != nil {
		for key, tagFn := range cfg.tagFns {
			if tagFn != nil {
				opts = append(opts, tracer.Tag(key, tagFn(db)))
			}
		}
	}

	span, _ := tracer.StartSpanFromContext(ctx, operationName, opts...)
	var dbErr error
	if cfg.errCheck(db.Error) {
		dbErr = db.Error
	}
	span.Finish(tracer.WithError(dbErr))
}
