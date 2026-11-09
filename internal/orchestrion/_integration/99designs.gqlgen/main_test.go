// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gqlgen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestServersInMain covers gqlgen servers created in package main, which the
// harness suites never build. Each server must report exactly one graphql.query
// span: a second one means the tracer was added twice.
func TestServersInMain(t *testing.T) {
	agent := maintest.Run(t, "./99designs.gqlgen/mainapp")
	for _, operation := range []string{"NewQuery", "DefaultServerQuery"} {
		require.Equalf(t, 1, countQuerySpans(agent, operation), "graphql.query spans for %s", operation)
	}
}

// countQuerySpans counts the graphql.query spans for the named operation. The
// condition never matches, so FindSpan visits every span.
func countQuerySpans(agent *maintest.Agent, operation string) int {
	var n int
	agent.FindSpan(agenttest.With().Condition("count", func(s *agenttest.Span) bool {
		if s.Operation == "graphql.query" && strings.Contains(s.Resource, "query "+operation+" ") {
			n++
		}
		return false
	}))
	return n
}
