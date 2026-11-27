// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build (linux || !githubci) && !windows

package kafka

import (
	"context"
	"slices"
	"testing"

	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

// TestDataStreamsOffsets checks the instrumentation of Producer.Events and of
// the three commit methods. They only track offsets for Data Streams
// Monitoring, so no span shows whether they work.
func TestDataStreamsOffsets(t *testing.T) {
	require.True(t, built.WithOrchestrion || otelc.Enabled(),
		"this test must be run with either orchestrion or otelc enabled")
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	mt := mocktracer.Start()
	defer mt.Stop()
	var cluster mockCluster
	cluster.Setup(context.Background(), t)

	// Messages sent through ProduceChannel report to Producer.Events, so the
	// produce offset is tracked only if Events returns the traced channel.
	p, err := kafka.NewProducer(cluster.producerConfig())
	require.NoError(t, err)
	p.ProduceChannel() <- mockMessage("dsm")
	requireDelivered(t, p.Events())
	p.Close()
	requireBacklog(t, mt, "type:kafka_produce")

	for _, tc := range []struct {
		name   string
		commit func(c *kafka.Consumer, msg *kafka.Message) error
	}{
		{"Commit", func(c *kafka.Consumer, msg *kafka.Message) error {
			if _, err := c.StoreMessage(msg); err != nil {
				return err
			}
			_, err := c.Commit()
			return err
		}},
		{"CommitMessage", func(c *kafka.Consumer, msg *kafka.Message) error {
			_, err := c.CommitMessage(msg)
			return err
		}},
		{"CommitOffsets", func(c *kafka.Consumer, msg *kafka.Message) error {
			tp := msg.TopicPartition
			tp.Offset++
			_, err := c.CommitOffsets([]kafka.TopicPartition{tp})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := "dsm-" + tc.name
			c, err := kafka.NewConsumer(cluster.consumerConfig(group))
			require.NoError(t, err)
			assign(t, c)
			msg := pollMessage(t, c.Poll)
			require.NoError(t, tc.commit(c, msg))
			require.NoError(t, c.Close())
			requireBacklog(t, mt, "type:kafka_commit", "consumer_group:"+group)
		})
	}
}

// requireBacklog checks that the tracer sent a Data Streams backlog with all
// of tags.
func requireBacklog(t *testing.T, mt mocktracer.Tracer, tags ...string) {
	t.Helper()
	var sent [][]string
	for _, b := range mt.SentDSMBacklogs() {
		if !slices.ContainsFunc(tags, func(tag string) bool { return !slices.Contains(b.Tags, tag) }) {
			return
		}
		sent = append(sent, b.Tags)
	}
	require.Failf(t, "no Data Streams backlog", "want tags %v, sent %v", tags, sent)
}
