// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package pg

import (
	"context"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/go-pg/pg/v10"
	"github.com/stretchr/testify/assert"
)

func TestImplementsHook(t *testing.T) {
	var _ pg.QueryHook = (*queryHook)(nil)
}

func TestSelect(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	conn := pg.Connect(&pg.Options{
		User:     "postgres",
		Database: "postgres",
	})

	Wrap(conn)

	parentSpan, ctx := tracer.StartSpanFromContext(context.Background(), "http.request",
		tracer.ServiceName("fake-http-server"),
		tracer.SpanType(ext.SpanTypeWeb),
	)

	var n int
	// Using WithContext will make the postgres span a child of
	// the span inside ctx (parentSpan)
	res, err := conn.WithContext(ctx).QueryOne(pg.Scan(&n), "SELECT 1")
	parentSpan.Finish()
	spans := mt.FinishedSpans()

	assert.Equal(1, res.RowsAffected())
	assert.Equal(1, res.RowsReturned())
	assert.Equal(2, len(spans))
	assert.Equal(nil, err)
	assert.Equal(1, n)
	assert.Equal("go-pg", spans[0].OperationName())
	assert.Equal("http.request", spans[1].OperationName())
}
