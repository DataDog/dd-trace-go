package tracertest

import (
	"testing"
	_ "unsafe"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
)

//go:linkname Start github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.startInspectableTracer
func Start(testing.TB, agenttest.Agent, ...tracer.StartOption) (tracer.Tracer, error)

//go:linkname Bootstrap github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.bootstrapInspectableTracer
func Bootstrap(testing.TB, ...tracer.StartOption) (tracer.Tracer, agenttest.Agent, error)

// StartAgent creates and starts an agent pre-configured with APM trace handlers.
// Use this when you need to register additional handlers (e.g. LLMObs) before
// starting the tracer with [Start].
//
//go:linkname StartAgent github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.startAgentTest
func StartAgent(testing.TB) (agenttest.Agent, error)
