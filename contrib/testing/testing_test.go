// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package testing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

func TestStatus(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	t.Run("pass", func(t *testing.T) {
		span, ctx := StartSpanFromContext(context.Background(), t)
		defer Finish(ctx, t)

		span.SetTag("k", "1")
	})

	t.Run("skip", func(t *testing.T) {
		span, ctx := StartSpanFromContext(context.Background(), t)
		defer Finish(ctx, t)

		span.SetTag("k", "2")
		t.Skip("good reason")
	})

	/* FIXME find a better way to execute failing test
	t.Run("fail", func(t *testing.T) {
		span, ctx := StartSpanFromContext(context.Background(), t)
		defer Finish(ctx, t)

		t.Fail()
		span.SetTag("k", "3")
	})
	*/

	assert := assert.New(t)

	spans := mt.FinishedSpans()
	assert.Equal(2, len(spans))

	s := spans[0]
	assert.Equal("test", s.OperationName())
	assert.Equal("TestStatus/pass", s.Tag(ext.TestName))
	assert.Equal(ext.TestStatusPass, s.Tag(ext.TestStatus))
	assert.Equal("1", s.Tag("k"))

	s = spans[1]
	assert.Equal("test", s.OperationName())
	assert.Equal("TestStatus/skip", s.Tag(ext.TestName))
	assert.Equal(ext.TestStatusSkip, s.Tag(ext.TestStatus))
	assert.Equal("2", s.Tag("k"))

	/*
	s = spans[2]
	assert.Equal("test", s.OperationName())
	assert.Equal("TestStatus/fail", s.Tag(ext.TestName))
	assert.Equal(ext.TestStatusFail, s.Tag(ext.TestStatus))
	assert.Equal("3", s.Tag("k"))
	*/
}