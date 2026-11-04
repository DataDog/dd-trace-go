// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awsv1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestSessionsInMain covers sessions built in package main, in a package-level
// var, through an aliased import and in a dependency module, and a session the
// application also wraps with the contrib. The harness suites never build a
// package main.
func TestSessionsInMain(t *testing.T) {
	agent := maintest.Run(t, "./aws.v1/mainapp")

	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")

	services := map[string]int{}
	for _, span := range lister.Spans() {
		if span.Operation == "dynamodb.command" {
			assert.Equal(t, "aws/aws-sdk-go/aws", span.Meta["component"])
			services[span.Service]++
		}
	}
	// One span per ListTables call: five sessions traced by the rule alone,
	// and one the application also wrapped, reported with its own service.
	assert.Equal(t, map[string]int{
		"aws.dynamodb":     9,
		"aws-user-service": 1,
	}, services)
}
