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

	twirptrace "github.com/DataDog/dd-trace-go/contrib/twitchtv/twirp/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

const contribServiceName = "twirp-contrib"

// TestCaseContribHooks checks that a server given the contrib's hooks gets
// one twirp span, with the contrib's options.
type TestCaseContribHooks struct {
	client example.Haberdasher
}

func (tc *TestCaseContribHooks) Setup(_ context.Context, t *testing.T) {
	hooks := twirptrace.NewServerHooks(twirptrace.WithService(contribServiceName))
	tc.client = serve(t, example.NewHaberdasherServer(&randomHaberdasher{}, twirp.WithServerHooks(hooks)))
}

func (tc *TestCaseContribHooks) Run(ctx context.Context, t *testing.T) {
	makeHat(ctx, t, tc.client)
}

func (*TestCaseContribHooks) ExpectedSpanCount() int {
	return spansPerRequest
}

func (*TestCaseContribHooks) ExpectedTraces() trace.Traces {
	traces := expectedTraces()
	traces[0].Children[0].Children[0].Tags["service"] = contribServiceName
	return traces
}
