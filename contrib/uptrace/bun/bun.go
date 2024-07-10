// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

// Package bun provides helper functions for tracing the github.com/uptrace/bun package (https://github.com/uptrace/bun).
package bun

import (
	"context"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
)

const (
	componentName      = "uptrace/bun"
	defaultServiceName = "bun.db"
)

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/uptrace/bun")
}

// Wrap augments the given DB with tracing.
func Wrap(db *bun.DB, opts ...Option) {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	log.Debug("contrib/uptrace/bun: Wrapping Database")
	db.AddQueryHook(&queryHook{cfg: cfg})
}

type queryHook struct {
	cfg *config
}

var _ bun.QueryHook = (*queryHook)(nil)

// BeforeQuery starts a span before a query is executed.
func (qh *queryHook) BeforeQuery(ctx context.Context, qe *bun.QueryEvent) context.Context {
	var dbSystem string
	switch qe.DB.Dialect().Name() {
	case dialect.PG:
		dbSystem = ext.DBSystemPostgreSQL
	case dialect.MySQL:
		dbSystem = ext.DBSystemMySQL
	case dialect.MSSQL:
		dbSystem = ext.DBSystemMicrosoftSQLServer
	default:
		dbSystem = ext.DBSystemOtherSQL
	}
	var (
		query = qe.Query
		opts  = []ddtrace.StartSpanOption{
			tracer.SpanType(ext.SpanTypeSQL),
			tracer.ResourceName(string(query)),
			tracer.ServiceName(qh.cfg.serviceName),
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.DBSystem, dbSystem),
		}
	)
	_, ctx = tracer.StartSpanFromContext(ctx, "bun.query", opts...)
	return ctx
}

// AfterQuery finishes a span when a query returns.
func (qh *queryHook) AfterQuery(ctx context.Context, qe *bun.QueryEvent) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	span.Finish(tracer.WithError(qe.Err))
}
