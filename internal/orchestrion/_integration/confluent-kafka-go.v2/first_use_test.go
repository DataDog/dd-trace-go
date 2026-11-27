// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package kafka

import (
	"sync"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/contrib/confluentinc/confluent-kafka-go/kafkatrace"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestConcurrentFirstUse produces from several goroutines on a producer that
// has not been used yet, so they all set up the lazily created tracer at once.
// CI runs it with -race (see .github/workflows/orchestrion.yml). No broker is
// needed: the messages are only queued.
func TestConcurrentFirstUse(t *testing.T) {
	require.True(t, built.WithOrchestrion || otelc.Enabled(),
		"this test must be run with either orchestrion or otelc enabled")

	// Orchestrion sets up the tracer on the first use of the producer, otelc
	// in kafka.NewProducer, so count from before the producer is created.
	tracers := kafkatrace.TracersCreated()
	p, err := kafka.NewProducer(&kafka.ConfigMap{"bootstrap.servers": "127.0.0.1:1"})
	require.NoError(t, err)

	topic := "first_use_test"
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			<-start
			msg := &kafka.Message{
				TopicPartition: kafka.TopicPartition{Topic: &topic, Partition: kafka.PartitionAny},
				Value:          []byte("value"),
			}
			_ = p.Produce(msg, nil)
		})
	}
	close(start)
	wg.Wait()
	// -race only reports a double setup that races, so also count them.
	require.EqualValues(t, 1, kafkatrace.TracersCreated()-tracers, "the producer must set up one tracer")
	p.Close()
}

// TestCloseUnused closes a producer that has never been used, and one whose
// first use races with Close.
func TestCloseUnused(t *testing.T) {
	require.True(t, built.WithOrchestrion || otelc.Enabled(),
		"this test must be run with either orchestrion or otelc enabled")

	p, err := kafka.NewProducer(&kafka.ConfigMap{"bootstrap.servers": "127.0.0.1:1"})
	require.NoError(t, err)
	p.Close()

	p, err = kafka.NewProducer(&kafka.ConfigMap{"bootstrap.servers": "127.0.0.1:1"})
	require.NoError(t, err)
	var wg sync.WaitGroup
	wg.Go(func() { _ = p.Events() })
	p.Close()
	wg.Wait()
}
