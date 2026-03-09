// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mcpgo

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/llmobstest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
)

// testTracer creates a tracer with LLMObs enabled for integration tests.
// It uses Bootstrap so the global tracer is set, allowing tracer.Flush() to work.
func testTracer(t *testing.T, opts ...tracer.StartOption) *llmobstest.Collector {
	t.Helper()
	coll := llmobstest.New(t)
	o := append([]tracer.StartOption{
		tracer.WithLLMObsEnabled(true),
		tracer.WithLLMObsMLApp("test-mcp-app"),
		tracer.WithLogStartup(false),
		coll.TracerOption(),
	}, opts...)
	_, _, err := tracertest.Bootstrap(t, o...)
	require.NoError(t, err)
	return coll
}

// mockSession is a simple mock implementation of server.ClientSession for testing
type mockSession struct {
	id             string
	initialized    bool
	notificationCh chan mcp.JSONRPCNotification
}

func (m *mockSession) SessionID() string {
	return m.id
}

func (m *mockSession) Initialize() {
	m.initialized = true
	m.notificationCh = make(chan mcp.JSONRPCNotification, 10)
}

func (m *mockSession) Initialized() bool {
	return m.initialized
}

func (m *mockSession) NotificationChannel() chan<- mcp.JSONRPCNotification {
	return m.notificationCh
}
