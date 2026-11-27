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
	franzkgo "github.com/twmb/franz-go/pkg/kgo"
)

// TestCaseAliasedImport checks that clients are traced when franz-go is
// imported under another name.
type TestCaseAliasedImport struct{ TestCase }

func (tc *TestCaseAliasedImport) Setup(_ context.Context, t *testing.T) {
	tc.setup(t, "twmb_franz_go_alias_topic", "twmb_franz_go_alias_group")
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	producer, err := franzkgo.NewClient(franzkgo.SeedBrokers(tc.addr))
	require.NoError(t, err)
	consumer, err := franzkgo.NewClient(
		franzkgo.SeedBrokers(tc.addr),
		franzkgo.ConsumeTopics(tc.topic),
		franzkgo.ConsumerGroup(tc.group),
	)
	require.NoError(t, err)
	tc.roundTrip(ctx, t, producer, consumer)
}
