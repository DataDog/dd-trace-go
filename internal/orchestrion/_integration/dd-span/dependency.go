// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ddspan

import (
	"context"
	"testing"

	"example.com/ddspandep"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a //dd:span function in a dependency module,
// not in the module being built, is traced.
type TestCaseDependency struct{}

func (*TestCaseDependency) Setup(context.Context, *testing.T) {}

func (*TestCaseDependency) Run(ctx context.Context, _ *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()
	ddspandep.Work(ctx)
}

func (*TestCaseDependency) ExpectedSpanCount() int {
	return 2
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name": "Work",
					},
					Meta: map[string]string{
						"function-name": "Work",
						"dep":           "ddspandep",
					},
				},
			},
		},
	}
}
