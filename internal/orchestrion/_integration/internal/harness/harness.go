// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

package harness

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/agent"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"
)

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
	// The tracer is not yet started when Setup is executed.
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

	mockAgent := agent.New(t)

	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(context.Background(), deadline)
		defer cancel()
	}

	t.Log("Running setup")
	tc.Setup(ctx, t)
	mockAgent.Start(t)

	t.Log("Running test")
	tc.Run(ctx, t)

	got := mockAgent.Traces(t)
	t.Logf("Received %d traces", len(got))
	for i, tr := range got {
		t.Logf("[%d] Trace contains a total of %d spans:\n%v", i, tr.NumSpans(), tr)
	}

	want := tc.ExpectedTraces()

	for _, expected := range want {
		expected.RequireAnyMatch(t, got)
	}
	t.Logf("Trace simplified versions:\nWant:\n%s\nGot:\n%s", trace.ToSimplified(want), trace.ToSimplified(got))
}
