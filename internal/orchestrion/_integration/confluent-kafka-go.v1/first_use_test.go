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
	"github.com/confluentinc/confluent-kafka-go/kafka"
	"github.com/stretchr/testify/require"
)

// TestConcurrentFirstUse produces from several goroutines on a producer that
// has not been used yet, so they all set up the lazily created tracer at once.
// CI runs it with -race (see .github/workflows/orchestrion.yml). No broker is
// needed: the messages are only queued.
func TestConcurrentFirstUse(t *testing.T) {
	require.True(t, built.WithOrchestrion, "this test must be run with orchestrion enabled")

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
	p.Close()
}

// TestCloseUnused closes a producer that has never been used, and one whose
// first use races with Close.
func TestCloseUnused(t *testing.T) {
	require.True(t, built.WithOrchestrion, "this test must be run with orchestrion enabled")

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
