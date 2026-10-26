// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awsv2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/maintest"
)

// TestConfigsInMain covers configs built in package main, through an aliased
// import, LoadDefaultConfig (also with API options) and a dependency module,
// and configs the application also traces with the contrib. The harness suites
// never build a package main.
func TestConfigsInMain(t *testing.T) {
	agent := maintest.Run(t, "./aws.v2/mainapp")

	lister, ok := agent.Agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")

	services := map[string]int{}
	for _, span := range lister.Spans() {
		if span.Operation == "DynamoDB.request" {
			assert.Equal(t, "aws/aws-sdk-go-v2/aws", span.Meta["component"])
			services[span.Service]++
		}
	}
	// One span per ListTables call: ten configs traced by the rules alone,
	// and three the application also traced, reported with its own service.
	assert.Equal(t, map[string]int{
		"aws.DynamoDB":     10,
		"aws-user-service": 3,
	}, services)
}
