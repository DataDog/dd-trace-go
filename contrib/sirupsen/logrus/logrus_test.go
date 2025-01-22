// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package logrus

import (
	"context"
	"strconv"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestFire128BitEnabled(t *testing.T) {
	// By default, trace IDs are logged in 128bit format
	tracer.Start()
	defer tracer.Stop()
	_, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	hook := &DDContextLogHook{}
	e := logrus.NewEntry(logrus.New())
	e.Context = sctx
	err := hook.Fire(e)
	assert.NoError(t, err)

	ctxW3c, ok := sctx.(ddtrace.SpanContextW3C)
	assert.True(t, ok)
	assert.Equal(t, strconv.FormatUint(ctxW3c.TraceID(), 10), e.Data["dd.trace_id"])
	assert.Equal(t, strconv.FormatUint(ctxW3c.SpanID(), 10), e.Data["dd.span_id"])
}

func TestFire128BitDisabled(t *testing.T) {
	// By default, trace IDs are logged in 128bit format
	t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")
	tracer.Start()
	defer tracer.Stop()
	_, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	hook := &DDContextLogHook{}
	e := logrus.NewEntry(logrus.New())
	e.Context = sctx
	err := hook.Fire(e)
	assert.NoError(t, err)

	assert.Equal(t, "1234", e.Data["dd.trace_id"])
	assert.Equal(t, "1234", e.Data["dd.span_id"])
}
