// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package twirp

import (
	"context"
	"testing"

	twirpv8 "github.com/twitchtv/twirp"
	"github.com/twitchtv/twirp/example"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a twirp.ServerOptions value literal is
// instrumented when twirp is imported under another name. The server takes
// the literal's hooks, so it has no twirp span if the literal is missed.
type TestCaseAliasedImport struct {
	client example.Haberdasher
}

func (tc *TestCaseAliasedImport) Setup(_ context.Context, t *testing.T) {
	opts := twirpv8.ServerOptions{}
	tc.client = serve(t, example.NewHaberdasherServer(&randomHaberdasher{}, twirpv8.WithServerHooks(opts.Hooks)))
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	makeHat(ctx, t, tc.client)
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return spansPerRequest
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return expectedTraces()
}
