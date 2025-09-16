package llmobs_test

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils/testtracer"
	"github.com/DataDog/dd-trace-go/v2/internal/llmobs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	mlApp = "gotest"
)

func testTracer(t *testing.T) *testtracer.TestTracer {
	return testtracer.Start(t,
		testtracer.WithTracerStartOpts(
			tracer.WithLLMObsEnabled(true),
			tracer.WithLLMObsMLApp(mlApp),
		),
		testtracer.WithAgentInfoResponse(testtracer.AgentInfo{
			Endpoints: []string{"/evp_proxy/v2/"},
		}),
	)
}

func TestStartSpan(t *testing.T) {
	tt := testTracer(t)
	defer tt.Stop()

	ll, err := llmobs.ActiveLLMObs()
	require.NoError(t, err)

	ctx := context.Background()
	span, ctx := ll.StartSpan(ctx, llmobs.SpanKindLLM, "llm-1")
	span.Finish()

	spans := tt.WaitForSpans(t, 1)
	s0 := spans[0]
	assert.Equal(t, "llm-1", s0.Name)

	llmSpans := tt.WaitForLLMObsSpans(t, 1)
	l0 := llmSpans[0]
	assert.Equal(t, "llm-1", l0.Name)
}

// TODO(rarguelloF): test context propagation
