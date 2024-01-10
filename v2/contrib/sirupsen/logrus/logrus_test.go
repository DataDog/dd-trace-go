// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package logrus

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestFire(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()
	sp, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	hook := &DDContextLogHook{}
	e := logrus.NewEntry(logrus.New())
	e.Context = sctx
	err := hook.Fire(e)

	assert.NoError(t, err)
	assert.Equal(t, sp.Context().TraceID(), e.Data["dd.trace_id"])
	assert.Equal(t, sp.Context().SpanID(), e.Data["dd.span_id"])
}
