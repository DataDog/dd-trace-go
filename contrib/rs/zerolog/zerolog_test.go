// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package zerolog

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestRun(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()
	sp, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	buf := strings.Builder{}
	zlog := zerolog.New(&buf).
		Hook(&DDContextLogHook{})
	zlog.Info().
		Ctx(sctx).
		Send()

	// By default, trace IDs are logged in 128bit format
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(buf.String()), &data); err != nil {
		assert.Fail(t, err.Error())
	}
	assert.Equal(t, sp.Context().TraceID(), data["dd.trace_id"])
	assert.Equal(t, strconv.FormatUint(sp.Context().SpanID(), 10), data["dd.span_id"])
}

func TestRun128BitDisabled(t *testing.T) {
	t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "false")

	// Re-initialize to account for race condition between setting env var in the test and reading it in the contrib
	cfg = newConfig()

	tracer.Start()
	defer tracer.Stop()
	sp, sctx := tracer.StartSpanFromContext(context.Background(), "testSpan", tracer.WithSpanID(1234))

	buf := strings.Builder{}
	zlog := zerolog.New(&buf).
		Hook(&DDContextLogHook{})
	zlog.Info().
		Ctx(sctx).
		Send()

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(buf.String()), &data); err != nil {
		assert.Fail(t, err.Error())
	}
	assert.Equal(t, strconv.FormatUint(sp.Context().TraceIDLower(), 10), data["dd.trace_id"])
	assert.Equal(t, strconv.FormatUint(sp.Context().SpanID(), 10), data["dd.span_id"])
}
