// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package aerospikev7

import (
	"context"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/harness"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestExactSpans runs each test case and checks that it gives exactly the
// spans it expects, no more. The harness only checks that the expected spans
// are there, and it also counts the spans of the container setup.
func TestExactSpans(t *testing.T) {
	require.True(t, built.WithOrchestrion || otelc.Enabled(),
		"this test must be run with either orchestrion or otelc enabled")

	for name, tc := range map[string]harness.TestCase{
		"Basic":         new(TestCase),
		"Goroutine":     new(TestCaseGoroutine),
		"ValueReceiver": new(TestCaseValueReceiver),
		"ScanAll":       new(TestCaseScanAll),
		"Query":         new(TestCaseQuery),
		"LibraryCalls":  new(TestCaseLibraryCalls),
		"CallForms":     new(TestCaseCallForms),
		"GoDefer":       new(TestCaseGoDefer),
		"WrappedClient": new(TestCaseWrappedClient),
		"AliasedImport": new(TestCaseAliasedImport),
		"Dependency":    new(TestCaseDependency),
		"Embedded":      new(TestCaseEmbedded),
	} {
		t.Run(name, func(t *testing.T) {
			tc.Setup(context.Background(), t)
			if t.Skipped() {
				return
			}
			tr, agent, err := tracertest.Bootstrap(t,
				tracer.WithSampler(tracer.NewAllSampler()),
				tracer.WithLogStartup(false),
			)
			require.NoError(t, err)
			tc.Run(context.Background(), t)
			tr.Stop()

			lister, ok := agent.(agenttest.SpanLister)
			require.True(t, ok, "the test agent must list its spans")
			var got []string
			for _, s := range lister.Spans() {
				if s.Operation == "test.root" || s.Meta["component"] == "aerospike/aerospike-client-go.v7" {
					got = append(got, s.Operation+" "+s.Resource)
				}
			}
			require.ElementsMatch(t, spanNames(tc.ExpectedTraces()), got)
		})
	}
}

func spanNames(traces trace.Traces) []string {
	names := make([]string, 0, len(traces))
	for _, tr := range traces {
		name := tr.Tags["name"].(string)
		resource, ok := tr.Tags["resource"].(string)
		if !ok {
			resource = name
		}
		names = append(names, name+" "+resource)
		names = append(names, spanNames(tr.Children)...)
	}
	return names
}
