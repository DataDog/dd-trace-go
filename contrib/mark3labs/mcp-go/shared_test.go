// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mcpgo

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
)

// testTracer creates a testtracer with LLMObs enabled for integration tests
func testTracer(t *testing.T, opts ...testtracer.Option) *testtracer.TestTracer {
	defaultOpts := []testtracer.Option{
		testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp("test-mcp-app"),
			tracer.WithLogStartup(false),
		),
		testtracer.WithAgentInfoResponse(testtracer.AgentInfo{
			Endpoints: []string{"/evp_proxy/v2/"},
		}),
	}
	allOpts := append(defaultOpts, opts...)
	tt := testtracer.Start(t, allOpts...)
	t.Cleanup(tt.Stop)
	return tt
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
