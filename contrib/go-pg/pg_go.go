// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package pg

import (
	"context"
	"time"

	"github.com/go-pg/pg/v10"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const gopgStartSpan = "dd-trace-go:span"

// Hook add QueryHook to existing go_pg.DB instance
func Hook(db *pg.DB) *pg.DB {
	db.AddQueryHook(&QueryHook{})
	return db
}

// QueryHook for go_pg
type QueryHook struct{}

// BeforeQuery is executed before query is sent
// Start measure, when query is started
func (h *QueryHook) BeforeQuery(ctx context.Context, qe *pg.QueryEvent) (context.Context, error) {
	if qe.Stash == nil {
		qe.Stash = make(map[interface{}]interface{})
	}

	unformatedSQL, _ := qe.UnformattedQuery()

	opts := []ddtrace.StartSpanOption{
		tracer.StartTime(time.Now()),
		tracer.SpanType(ext.SpanTypeSQL),
		tracer.ResourceName(string(unformatedSQL)),
	}
	_, ctx = tracer.StartSpanFromContext(ctx, "gopg", opts...)
	return ctx, qe.Err
}

// AfterQuery is executed after query is finished
func (h *QueryHook) AfterQuery(ctx context.Context, qe *pg.QueryEvent) error {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return nil
	}

	span.Finish(tracer.WithError(qe.Err))

	return qe.Err
}
