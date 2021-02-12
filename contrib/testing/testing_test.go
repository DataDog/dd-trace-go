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
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestStatus(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	t.Run("pass", func(t *testing.T) {
		ctx, finish := StartSpanWithFinish(context.Background(), t, WithSkipFrames(1))
		defer finish()

		span, _ := tracer.SpanFromContext(ctx)
		span.SetTag("k", "1")
	})

	t.Run("skip", func(t *testing.T) {
		ctx, finish := StartSpanWithFinish(context.Background(), t)
		defer finish()

		span, _ := tracer.SpanFromContext(ctx)
		span.SetTag("k", "2")
		t.Skip("good reason")
	})

	assert := assert.New(t)

	spans := mt.FinishedSpans()
	assert.Equal(2, len(spans))

	s := spans[0]
	assert.Equal("test", s.OperationName())
	assert.Equal("TestStatus/pass", s.Tag(ext.TestName))
	assert.Contains(s.Tag(ext.TestSuite), "contrib/testing/testing_test.go")
	assert.Equal(ext.TestStatusPass, s.Tag(ext.TestStatus))
	assert.Equal("1", s.Tag("k"))
	assert.NotEmpty(s.Tag(ext.GitRepositoryURL))
	assert.NotEmpty(s.Tag(ext.GitBranch))
	assert.NotEmpty(s.Tag(ext.GitCommitSHA))

	s = spans[1]
	assert.Equal("test", s.OperationName())
	assert.Equal("TestStatus/skip", s.Tag(ext.TestName))
	assert.Equal(ext.TestStatusSkip, s.Tag(ext.TestStatus))
	assert.Equal("2", s.Tag("k"))
}
