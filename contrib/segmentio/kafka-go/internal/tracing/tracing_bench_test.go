// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracing

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// simpleWriter and simpleMessage give StartProduceSpan/StartConsumeSpan
// something Writer/Message-shaped to work with without depending on a real
// segmentio/kafka-go client.
type simpleWriter struct{ topic string }

func (w simpleWriter) GetTopic() string { return w.topic }

type simpleMessage struct {
	topic     string
	partition int
	offset    int64
	headers   []Header
}

func (m *simpleMessage) GetValue() []byte      { return nil }
func (m *simpleMessage) GetKey() []byte        { return nil }
func (m *simpleMessage) GetHeaders() []Header  { return m.headers }
func (m *simpleMessage) SetHeaders(h []Header) { m.headers = h }
func (m *simpleMessage) GetTopic() string      { return m.topic }
func (m *simpleMessage) GetPartition() int     { return m.partition }
func (m *simpleMessage) GetOffset() int64      { return m.offset }

// BenchmarkStartProduceSpan measures Tracer.StartProduceSpan, which merges
// static, build-once tags via WithStartSpanConfig and per-message tags via
// a single WithTags map instead of one tracer.Tag closure per tag. It goes
// through a real tracer.StartSpanFromContext call, like production code, so
// option values escape to the heap the same way they do in production
// instead of being optimized away by inlining (caller and
// tracer.Tag/WithStartSpanConfig live in different packages here, same as
// valkey's BenchmarkStartSpan).
func BenchmarkStartProduceSpan(b *testing.B) {
	err := tracer.Start(tracer.WithTestDefaults(nil))
	if err != nil {
		b.Fatal(err)
	}
	defer tracer.Stop()

	tr := NewTracer(KafkaConfig{})
	w := simpleWriter{topic: "test-topic"}
	msg := &simpleMessage{topic: "test-topic"}
	ctx := context.Background()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		span := tr.StartProduceSpan(ctx, w, msg)
		span.Finish()
	}
}
