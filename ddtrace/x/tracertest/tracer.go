// Package tracertest provides functions to start an inspectable tracer backed
// by an in-process mock agent. It is intended for integration-style tests where
// you want to verify actual spans produced by instrumented code without relying
// on an external agent process or timeout-based polling.
//
// The simplest way to get started is [Bootstrap], which creates both the agent
// and the tracer in a single call:
//
//	tr, agent, err := tracertest.Bootstrap(t)
//	require.NoError(t, err)
//	// ... exercise instrumented code ...
//	tr.Flush()
//	agent.RequireSpan(t, agenttest.With().Operation("http.request"))
//
// For advanced use cases (e.g. registering custom agent handlers or LLMObs
// collectors before starting the tracer), use [StartAgent] followed by [Start].
package tracertest

import (
	"testing"
	_ "unsafe"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
)

// Start creates an inspectable tracer using the provided agent and options. The
// tracer is stopped automatically when the test ends. Unlike [Bootstrap], this
// function does not set the global tracer, so it is safe to use in parallel
// tests that pass the tracer explicitly.
//
//go:linkname Start github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.startInspectableTracer
func Start(testing.TB, agenttest.Agent, ...tracer.StartOption) (tracer.Tracer, error)

// Bootstrap creates a mock agent and an inspectable tracer in a single call,
// and sets the tracer as the global tracer. The tracer and global state are
// cleaned up automatically when the test ends.
//
//go:linkname Bootstrap github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.bootstrapInspectableTracer
func Bootstrap(testing.TB, ...tracer.StartOption) (tracer.Tracer, agenttest.Agent, error)

// StartAgent creates and starts an agent pre-configured with APM trace handlers.
// Use this when you need to register additional handlers (e.g. LLMObs) before
// starting the tracer with [Start].
//
//go:linkname StartAgent github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.startAgentTest
func StartAgent(testing.TB) (agenttest.Agent, error)
