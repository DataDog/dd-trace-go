// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package gcppubsub

import (
	"context"
	"testing"

	"cloud.google.com/go/pubsub/pstest"
	"example.com/pubsubdep/v1admin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that an admin client created in a dependency
// module, not in the module being built, is traced.
type TestCaseDependency struct {
	adminBase
	srv *pstest.Server
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	tc.srv = pstest.NewServer()
	t.Cleanup(func() { assert.NoError(t, tc.srv.Close()) })
	tc.projectID = adminProject
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	defer span.Finish()

	require.NoError(t, v1admin.ListTopics(ctx, tc.projectID, emulatorOptions(tc.srv.Addr)...))
}

func (tc *TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name": "test.root",
			},
			Children: trace.Traces{
				tc.adminTrace("ListTopics", tc.projectPath()),
			},
		},
	}
}
