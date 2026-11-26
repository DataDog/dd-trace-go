// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package twirp

import (
	"context"
	"testing"

	"example.com/twirpdep/server"
	"github.com/twitchtv/twirp/example"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a twirp.ServerOptions literal in a dependency
// module, not in the module being built, is instrumented. The server takes
// the literal's hooks, so it has no twirp span if the literal is missed.
type TestCaseDependency struct {
	client example.Haberdasher
}

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	tc.client = serve(t, server.NewHaberdasherServer(&randomHaberdasher{}))
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	makeHat(ctx, t, tc.client)
}

func (*TestCaseDependency) ExpectedSpanCount() int {
	return spansPerRequest
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return expectedTraces()
}
