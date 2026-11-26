// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package kgo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func TestSecondTracingHookDisabled(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	first, second := newTracingHook(), newTracingHook()
	newOfflineClient(t, first, kgo.WithHooks(second))
	assert.False(t, first.disabled.Load())
	assert.True(t, second.disabled.Load())

	hooks := []*tracingHook{first, second}
	root, ctx := tracer.StartSpanFromContext(context.Background(), "root")
	r := &kgo.Record{Topic: "topic", Context: ctx}
	for _, h := range hooks {
		h.OnProduceRecordBuffered(r)
	}
	for _, h := range hooks {
		h.OnProduceRecordUnbuffered(r, nil)
	}
	root.Finish()
	for _, h := range hooks {
		h.OnFetchRecordUnbuffered(&kgo.Record{Topic: "topic", Context: context.Background()}, true)
	}
	for _, h := range hooks {
		h.OnClientClosed(nil)
	}

	ops := map[string]int{}
	for _, s := range mt.FinishedSpans() {
		ops[s.OperationName()]++
	}
	assert.Equal(t, map[string]int{"root": 1, "kafka.produce": 1, "kafka.consume": 1}, ops)
	assert.Empty(t, mt.OpenSpans())
}

// TestWithTracingTwiceFunctional checks a producer and a consumer that each
// pass WithTracing twice.
func TestWithTracingTwiceFunctional(t *testing.T) {
	topic := topicName(t)
	createTopicWithCleanup(t, topic)

	mt := mocktracer.Start()
	defer mt.Stop()

	producer, err := kgo.NewClient(kgo.SeedBrokers(kafkaBrokers...), WithTracing(), WithTracing())
	require.NoError(t, err)
	root, ctx := tracer.StartSpanFromContext(context.Background(), "root")
	record := &kgo.Record{Topic: topic, Key: []byte("key1"), Value: []byte("value1")}
	require.NoError(t, producer.ProduceSync(ctx, record).FirstErr())
	root.Finish()
	producer.Close()

	consumer, err := kgo.NewClient(
		kgo.SeedBrokers(kafkaBrokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		WithTracing(),
		WithTracing(),
	)
	require.NoError(t, err)
	fetchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fetches := consumer.PollFetches(fetchCtx)
	require.NoError(t, fetches.Err())
	require.Len(t, fetches.Records(), 1)
	consumer.Close()

	ops := map[string]int{}
	for _, s := range mt.FinishedSpans() {
		ops[s.OperationName()]++
	}
	assert.Equal(t, map[string]int{"root": 1, "kafka.produce": 1, "kafka.consume": 1}, ops)
	assert.Empty(t, mt.OpenSpans())
}

func TestShadowedWithoutHooksOptValue(t *testing.T) {
	assert.False(t, shadowed(noOptValues{}, newTracingHook()))
}

// noOptValues stands in for a client whose OptValue knows no option.
type noOptValues struct{}

func (noOptValues) OptValue(any) any { return nil }
