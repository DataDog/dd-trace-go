// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package twmb_franz_go

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// roundTrip produces one record with producer under a test.root span, then
// polls it with consumer. It closes both clients.
func (tc *TestCase) roundTrip(ctx context.Context, t *testing.T, producer, consumer *kgo.Client) {
	t.Helper()
	span, ctx := tracer.StartSpanFromContext(ctx, "test.root")
	record := &kgo.Record{Topic: tc.topic, Key: []byte("key1"), Value: []byte("Hello World!")}
	err := producer.ProduceSync(ctx, record).FirstErr()
	producer.Close()
	span.Finish()
	require.NoError(t, err)

	tc.poll(t, consumer)
}

// poll polls the record roundTrip produced with consumer, then closes it.
func (tc *TestCase) poll(t *testing.T, consumer *kgo.Client) {
	t.Helper()
	defer consumer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fetches := consumer.PollFetches(ctx)
	require.NoError(t, fetches.Err())
	records := fetches.Records()
	require.Len(t, records, 1)
	require.Equal(t, "Hello World!", string(records[0].Value))
}

func (tc *TestCase) consumerOpts() []kgo.Opt {
	return []kgo.Opt{
		kgo.SeedBrokers(tc.addr),
		kgo.ConsumeTopics(tc.topic),
		kgo.ConsumerGroup(tc.group),
	}
}
