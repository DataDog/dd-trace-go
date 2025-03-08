// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package logrus

import (
	"context"
	"strconv"
	"strings"
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

	traceID := strconv.FormatUint(uint64(1234), 16)
	assert.NoError(t, err)
	// v2 generates 128-bit trace IDs, so we need to compare only the last second half
	assert.True(t, strings.HasSuffix(e.Data["dd.trace_id"].(string), traceID))
	spanID, _ := strconv.ParseUint(e.Data["dd.span_id"].(string), 10, 64)
	assert.Equal(t, uint64(1234), spanID)
}
