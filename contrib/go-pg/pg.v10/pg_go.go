// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package pg

import (
	"context"
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/go-pg/pg/v10"
)

const componentName = "go-pg/pg.v10"

func init() {
	telemetry.LoadIntegration(componentName)
	tracer.MarkIntegrationImported("github.com/go-pg/pg/v10")
}

// Wrap augments the given DB with tracing.
func Wrap(db *pg.DB, opts ...Option) {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}
	log.Debug("contrib/go-pg/pg.v10: Wrapping Database")
	db.AddQueryHook(&queryHook{cfg: cfg})
}

type queryHook struct {
	cfg *config
}

// BeforeQuery implements pg.QueryHook.
func (h *queryHook) BeforeQuery(ctx context.Context, qe *pg.QueryEvent) (context.Context, error) {
	query, err := qe.UnformattedQuery()
	if err != nil {
		query = []byte("unknown")
	}

	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.ResourceName(string(query)),
		tracer.ServiceName(h.cfg.serviceName),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.DBSystem, ext.DBSystemPostgreSQL),
	}
	if !math.IsNaN(h.cfg.analyticsRate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, h.cfg.analyticsRate))
	}
	_, ctx = tracer.StartSpanFromContext(ctx, "go-pg", opts...)
	return ctx, qe.Err
}

// AfterQuery implements pg.QueryHook
func (h *queryHook) AfterQuery(ctx context.Context, qe *pg.QueryEvent) error {
	if span, ok := tracer.SpanFromContext(ctx); ok {
		span.Finish(tracer.WithError(qe.Err))
	}

	return qe.Err
}
