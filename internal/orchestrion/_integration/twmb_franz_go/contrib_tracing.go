// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package twmb_franz_go

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	kgotrace "github.com/DataDog/dd-trace-go/contrib/twmb/franz-go/v2"
)

// TestCaseContribTracing checks clients the application already traces with
// kgotrace.WithTracing. The instrumentation adds a second tracing hook to
// them, and the contrib keeps only the first one.
type TestCaseContribTracing struct{ TestCase }

func (tc *TestCaseContribTracing) Setup(_ context.Context, t *testing.T) {
	tc.setup(t, "twmb_franz_go_contrib_topic", "twmb_franz_go_contrib_group")
}

func (tc *TestCaseContribTracing) Run(ctx context.Context, t *testing.T) {
	producer, err := kgo.NewClient(kgo.SeedBrokers(tc.addr), kgotrace.WithTracing())
	require.NoError(t, err)
	consumer, err := kgo.NewClient(append(tc.consumerOpts(), kgotrace.WithTracing())...)
	require.NoError(t, err)
	tc.roundTrip(ctx, t, producer, consumer)
}
