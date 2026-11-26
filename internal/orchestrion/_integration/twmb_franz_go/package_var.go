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
)

// The producers are created during package initialization, before the tracer
// starts and with no seed brokers yet. Each test that uses one closes it and
// runs against a new Kafka container, so each has its own.
var (
	pkgProducer, pkgProducerErr           = kgo.NewClient()
	pkgProducerExact, pkgProducerExactErr = kgo.NewClient()
)

// TestCasePackageVar checks that a client created in a package-level variable
// is traced.
type TestCasePackageVar struct {
	TestCase
	exact bool
}

func (tc *TestCasePackageVar) Setup(_ context.Context, t *testing.T) {
	tc.setup(t, "twmb_franz_go_package_var_topic", "twmb_franz_go_package_var_group")
}

func (tc *TestCasePackageVar) Run(ctx context.Context, t *testing.T) {
	producer, err := pkgProducer, pkgProducerErr
	if tc.exact {
		producer, err = pkgProducerExact, pkgProducerExactErr
	}
	require.NoError(t, err)
	require.NoError(t, producer.UpdateSeedBrokers(tc.addr))
	consumer, err := kgo.NewClient(tc.consumerOpts()...)
	require.NoError(t, err)
	tc.roundTrip(ctx, t, producer, consumer)
}
