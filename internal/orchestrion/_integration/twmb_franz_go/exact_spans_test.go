// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package twmb_franz_go

import (
	"context"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

const componentName = "twmb/franz-go"

func (tc *TestCase) base() *TestCase { return tc }

// TestExactSpans checks the exact franz-go spans of each way to create a
// client. The suite's other tests match spans by subset, so they miss a
// record traced twice.
func TestExactSpans(t *testing.T) {
	require.True(t, built.WithOrchestrion || otelc.Enabled(),
		"this test must be run with either orchestrion or otelc enabled")
	containers.SkipIfProviderIsNotHealthy(t)

	cases := []struct {
		name     string
		tc       interface{ base() *TestCase }
		produced int
		consumed int
		// otelcOnly is the reason to skip the case under orchestrion.
		otelcOnly string
	}{
		{name: "Args", tc: &TestCase{}, produced: 1, consumed: 1},
		{name: "Ellipsis", tc: &TestCaseEllipsis{}, produced: 1, consumed: 1},
		{name: "NoArgs", tc: &TestCaseNoArgs{}, produced: 1},
		{name: "AliasedImport", tc: &TestCaseAliasedImport{}, produced: 1, consumed: 1},
		{name: "Dependency", tc: &TestCaseDependency{}, produced: 1, consumed: 1},
		{name: "PackageVar", tc: &TestCasePackageVar{exact: true}, produced: 1, consumed: 1},
		{name: "GroupTransactSession", tc: &TestCaseGroupTransactSession{}, produced: 1, consumed: 1, otelcOnly: skipGroupTransactSession},
		{name: "ContribTracing", tc: &TestCaseContribTracing{}, produced: 1, consumed: 1},
	}
	topics := make([]string, len(cases))
	for i, c := range cases {
		topics[i] = "twmb_franz_go_exact_" + c.name
	}
	_, addr := containers.StartKafkaTestContainer(t, topics)

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.otelcOnly != "" && !otelc.Enabled() {
				t.Skip(c.otelcOnly)
			}
			b := c.tc.base()
			b.addr = addr
			b.topic = topics[i]
			b.group = topics[i] + "_group"
			runner := c.tc.(interface {
				Run(context.Context, *testing.T)
			})
			spans := traced(t, func(ctx context.Context) { runner.Run(ctx, t) })

			root := filter(spans, "test.root")
			produced := filter(spans, "kafka.produce")
			consumed := filter(spans, "kafka.consume")
			require.Len(t, root, 1)
			require.Len(t, produced, c.produced)
			require.Len(t, consumed, c.consumed)
			require.Len(t, spans, 1+c.produced+c.consumed)
			for _, s := range produced {
				assert.Equal(t, root[0].SpanID, s.ParentID, "produce spans are children of the root")
			}
			for _, s := range consumed {
				assert.Equal(t, produced[0].SpanID, s.ParentID, "consume spans are children of the produce span")
			}
		})
	}
}

// traced runs fn with a fresh tracer and returns the franz-go and test.root
// spans it reported. Spans of other components, such as the net/http spans of
// the Docker API calls testcontainers makes in the background, are dropped.
func traced(t *testing.T, fn func(context.Context)) []*agenttest.Span {
	t.Helper()
	tr, agent, err := tracertest.Bootstrap(t,
		tracer.WithSampler(tracer.NewAllSampler()),
		tracer.WithLogStartup(false),
	)
	require.NoError(t, err)
	fn(context.Background())
	tr.Stop()
	lister, ok := agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")
	var spans []*agenttest.Span
	for _, s := range lister.Spans() {
		if s.Operation == "test.root" || s.Meta["component"] == componentName {
			spans = append(spans, s)
		}
	}
	return spans
}

func filter(spans []*agenttest.Span, operation string) []*agenttest.Span {
	var out []*agenttest.Span
	for _, s := range spans {
		if s.Operation == operation {
			out = append(out, s)
		}
	}
	return out
}
