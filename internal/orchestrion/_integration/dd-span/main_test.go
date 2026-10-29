// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ddspan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestSpansInMain covers //dd:span functions in package main. The harness
// suites never build a package main.
func TestSpansInMain(t *testing.T) {
	agent := maintest.Run(t, "./dd-span/mainapp")

	spans := make(map[string]*agenttest.Span)
	for _, s := range agent.Agent.(agenttest.SpanLister).Spans() {
		require.NotContains(t, spans, s.Operation, "two spans named %q", s.Operation)
		spans[s.Operation] = s
	}
	requireOperations(t, spans, "run", "work", "main.fail")

	assert.Zero(t, spans["run"].ParentID)
	requireParent(t, spans, "work", "run")
	requireParent(t, spans, "main.fail", "run")
	assert.Equal(t, map[string]string{"function-name": "run", "app": "main"}, userMeta(spans["run"]))
	assert.Equal(t, map[string]string{"function-name": "work", "app": "work"}, userMeta(spans["work"]))
	assert.EqualValues(t, 1, spans["main.fail"].Error)
	assert.Equal(t, "main error", spans["main.fail"].Meta["error.message"])
	assert.Equal(t, "fail", spans["main.fail"].Meta["function-name"])
}
