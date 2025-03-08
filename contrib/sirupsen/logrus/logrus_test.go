// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package logrus

import (
	"context"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestFire(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()
	_, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	hook := &DDContextLogHook{}
	e := logrus.NewEntry(logrus.New())
	e.Context = sctx
	err := hook.Fire(e)

	assert.NoError(t, err)
	assert.Equal(t, uint64(1234), e.Data["dd.trace_id"])
	assert.Equal(t, uint64(1234), e.Data["dd.span_id"])
}

func TestDDLogInjectionDisabled(t *testing.T) {
	t.Setenv(logInjection, "false")
	// Re-initialize to account for race condition between setting env var in the test and reading it in the contrib
	cfg = newConfig()

	tracer.Start()
	defer tracer.Stop()
	_, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	hook := &DDContextLogHook{}
	e := logrus.NewEntry(logrus.New())
	e.Context = sctx
	err := hook.Fire(e)

	assert.NoError(t, err)
	assert.NotContains(t, e.Data, "dd.trace_id")
	assert.NotContains(t, e.Data, "dd.span_id")
}
