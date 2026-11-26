// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package twmb_franz_go

import (
	"context"
	"testing"

	"example.com/franzgodep/client"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

// TestCaseDependency checks that clients created in a dependency module, not
// in the module being built, are traced.
type TestCaseDependency struct{ TestCase }

func (tc *TestCaseDependency) Setup(_ context.Context, t *testing.T) {
	tc.setup(t, "twmb_franz_go_dependency_topic", "twmb_franz_go_dependency_group")
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	producer, err := client.New(kgo.SeedBrokers(tc.addr))
	require.NoError(t, err)
	consumer, err := client.New(tc.consumerOpts()...)
	require.NoError(t, err)
	tc.roundTrip(ctx, t, producer, consumer)
}
