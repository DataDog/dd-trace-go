// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package twirp

import (
	"context"
	"testing"

	"github.com/twitchtv/twirp"
	"github.com/twitchtv/twirp/example"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// packageLevelOptions is built during package initialization.
var packageLevelOptions = &twirp.ServerOptions{}

// TestCasePackageLevelOptions checks that a literal in a package-level
// variable is instrumented. The server takes the literal's hooks, so it has
// no twirp span if the literal is missed.
type TestCasePackageLevelOptions struct {
	client example.Haberdasher
}

func (tc *TestCasePackageLevelOptions) Setup(_ context.Context, t *testing.T) {
	tc.client = serve(t, example.NewHaberdasherServer(&randomHaberdasher{}, twirp.WithServerHooks(packageLevelOptions.Hooks)))
}

func (tc *TestCasePackageLevelOptions) Run(ctx context.Context, t *testing.T) {
	makeHat(ctx, t, tc.client)
}

func (*TestCasePackageLevelOptions) ExpectedSpanCount() int {
	return spansPerRequest
}

func (*TestCasePackageLevelOptions) ExpectedTraces() trace.Traces {
	return expectedTraces()
}
