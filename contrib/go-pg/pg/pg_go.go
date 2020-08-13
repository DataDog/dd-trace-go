// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package pg

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/go-pg/pg/v10"
)

// Hook wraps the given pg.Connect with QueryHooks.
// go-pg support QueryHook, and this is used for achieve tracing sql.
func Hook(db *pg.DB) *pg.DB {
	db.AddQueryHook(&queryHook{})
	return db
}

type queryHook struct{}

// BeforeQuery is called, before query is executed,
// Span is created and stored to given context.Context.
func (h *queryHook) BeforeQuery(ctx context.Context, qe *pg.QueryEvent) (context.Context, error) {
	query, err := qe.UnformattedQuery()
	if err != nil {
		query = []byte("unknown")
	}

	opts := []ddtrace.StartSpanOption{
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.ResourceName(string(query)),
	}
	_, ctx = tracer.StartSpanFromContext(ctx, "go-pg", opts...)
	return ctx, qe.Err
}

// AfterQuery hook is Called after query is ended and finish
// span, which was created in BeforeQuery.
// If error is occurred while sql execution, then error is stored to span and is visible
// in Datadog APM.
func (h *queryHook) AfterQuery(ctx context.Context, qe *pg.QueryEvent) error {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return nil
	}
	span.Finish(tracer.WithError(qe.Err))

	return qe.Err
}
