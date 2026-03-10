// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCasePreBootstrap is an optional interface for test cases that need to
// configure the environment before the tracer is bootstrapped (e.g., to set
// AppSec rule files via env vars).
type TestCasePreBootstrap interface {
	PreBootstrap(context.Context, *testing.T)
}

// TestCase describes the general contract for tests. Each package in this
// directory is expected to export a [TestCase] structure implementing this
// interface.
type TestCase interface {
	// Setup is called before the test is run. It should be used to prepare any
	// the test for execution, such as starting up services (e.g, databse servers)
	// or setting up test data. The Setup function can call [testing.T.SkipNow] to
	// skip the test entirely, for example if prerequisites of its dependencies
	// are not satisfied by the test environment.
	//
	// The tracer is not started yet when Setup is executed.
	Setup(context.Context, *testing.T)

	// Run executes the test case after starting the tracer. This should perform
	// the necessary calls to produce trace information from injected
	// instrumentation, and assert on expected post-conditions (e.g, HTTP request
	// is expected to be successful, database call does not error out, etc...).
	// The tracer is shut down after the Run function returns, ensuring
	// outstanding spans are flushed to the agent.
	Run(context.Context, *testing.T)

	// ExpectedTraces returns a trace.Traces object describing all traces expected
	// to be produced by the [TestCase.Run] function. There should be one entry
	// per trace root span expected to be produced. Every item in the returned
	// [trace.Traces] must match at least one trace received by the agent during
	// the test run.
	ExpectedTraces() trace.Traces
}

func Run(t *testing.T, tc TestCase) {
	t.Helper()
	require.True(t, built.WithOrchestrion, "this test suite must be run with orchestrion enabled")

	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(context.Background(), deadline)
		defer cancel()
	}

	// Increase WAF timeout to avoid flakiness on slow CI hosts.
	t.Setenv("DD_APPSEC_WAF_TIMEOUT", "1s")
	// Neutralize API Security sampling to prevent test flakiness.
	t.Setenv("DD_API_SECURITY_SAMPLE_DELAY", "0")

	if pb, ok := tc.(TestCasePreBootstrap); ok {
		pb.PreBootstrap(ctx, t)
		if t.Skipped() {
			return
		}
	}

	// Bootstrap the inspectable tracer and its mock agent.
	tr, agent, err := tracertest.Bootstrap(t,
		tracer.WithSampler(tracer.NewAllSampler()),
		tracer.WithLogStartup(false),
		tracer.WithLogger(testLogger{t}),
		tracer.WithAppSecEnabled(true),
	)
	require.NoError(t, err)

	t.Log("Running setup")
	tc.Setup(ctx, t)

	t.Log("Running test")
	tc.Run(ctx, t)

	tr.Stop()

	t.Logf("Received %d spans", agent.CountSpans())
	requireTraceMatch(t, agent, tc.ExpectedTraces())
}

type testLogger struct {
	*testing.T
}

func (l testLogger) Log(msg string) {
	l.T.Log(msg)
}

func requireTraceMatch(t testing.TB, a agenttest.Agent, expected trace.Traces) {
	t.Helper()
	for _, exp := range expected {
		requireSpanMatch(t, a, exp, 0)
	}
}

func requireSpanMatch(t testing.TB, a agenttest.Agent, exp *trace.Trace, parentSpanID uint64) {
	t.Helper()
	m := traceToMatcher(exp)
	if parentSpanID != 0 {
		m = m.ParentOf(parentSpanID)
	}
	found := a.RequireSpan(t, m)
	for _, child := range exp.Children {
		requireSpanMatch(t, a, child, uint64(found.SpanID))
	}
}

func traceToMatcher(tr *trace.Trace) *agenttest.SpanMatch {
	m := agenttest.With()
	for k, v := range tr.Tags {
		switch k {
		case "name":
			if s, ok := v.(string); ok {
				m = m.Operation(s)
			}
		case "service":
			if s, ok := v.(string); ok {
				// On Windows, binary names include a .exe suffix which is stripped for comparison.
				m = m.Condition(fmt.Sprintf("Service == %q", s), func(sp *agenttest.Span) bool {
					return strings.TrimSuffix(sp.Service, ".exe") == s
				})
			}
		case "resource":
			if s, ok := v.(string); ok {
				m = m.Resource(s)
			}
		case "type":
			if s, ok := v.(string); ok {
				m = m.Type(s)
			}
		default:
			m = m.Tag(k, v)
		}
	}
	for k, v := range tr.Meta {
		m = m.Tag(k, v)
	}
	for k, v := range tr.Metrics {
		m = m.Tag(k, v)
	}
	return m
}
