// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package zerolog

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCase struct {
	logger *zerolog.Logger
	logs   *strings.Builder
}

func (tc *TestCase) Setup(_ context.Context, t *testing.T) {
	tc.logs = &strings.Builder{}
	logger := zerolog.New(tc.logs)
	tc.logger = &logger
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	tc.logger.Info().Ctx(ctx).Send()

	var data map[string]any
	if err := json.Unmarshal([]byte(tc.logs.String()), &data); err != nil {
		assert.Fail(t, err.Error())
	}
	if ok, _ := regexp.MatchString(`\d+`, data["dd.trace_id"].(string)); !ok {
		t.Errorf("no trace ID")
	}
	if ok, _ := regexp.MatchString(`\d+`, data["dd.span_id"].(string)); !ok {
		t.Errorf("no span ID")
	}
}

func (*TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{}
}
