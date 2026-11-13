// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package kafka

import (
	"context"
	"testing"

	"github.com/confluentinc/confluent-kafka-go/kafka"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// The clients are created during package initialization, before the tracer
// starts.
var (
	pkgCluster, pkgClusterErr   = kafka.NewMockCluster(1)
	pkgProducer, pkgProducerErr = kafka.NewProducer(&kafka.ConfigMap{"bootstrap.servers": pkgServers()})
	pkgConsumer, pkgConsumerErr = kafka.NewConsumer(&kafka.ConfigMap{
		"bootstrap.servers":        pkgServers(),
		"group.id":                 "package-var",
		"enable.auto.offset.store": false,
	})
)

func pkgServers() string {
	if pkgCluster == nil {
		return ""
	}
	return pkgCluster.BootstrapServers()
}

// TestCasePackageVar checks that clients created in package-level variables
// are traced once.
type TestCasePackageVar struct{ mockCluster }

func (tc *TestCasePackageVar) Setup(_ context.Context, t *testing.T) {
	require.NoError(t, pkgClusterErr)
	require.NoError(t, pkgProducerErr)
	require.NoError(t, pkgConsumerErr)
	t.Cleanup(pkgCluster.Close)
	tc.servers = pkgServers()
}

func (*TestCasePackageVar) Run(_ context.Context, t *testing.T) {
	delivery := make(chan kafka.Event, 1)
	require.NoError(t, pkgProducer.Produce(mockMessage("package-var"), delivery))
	requireDelivered(t, delivery)
	pkgProducer.Close()

	assign(t, pkgConsumer)
	msg := pollMessage(t, pkgConsumer.Poll)
	require.Equal(t, "package-var", string(msg.Key))
	require.NoError(t, pkgConsumer.Close())
}

func (tc *TestCasePackageVar) ExpectedTraces() trace.Traces {
	return trace.Traces{tc.mockTrace()}
}
func (*TestCasePackageVar) ExpectedSpanCount() int { return 2 }
