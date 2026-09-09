// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafkatrace

import (
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// simpleTopicPartition and simpleMessage give StartProduceSpan/StartConsumeSpan
// something Message-shaped to work with without depending on a real
// confluent-kafka-go client.
type simpleTopicPartition struct {
	topic     string
	partition int32
	offset    int64
}

func (tp simpleTopicPartition) GetTopic() string    { return tp.topic }
func (tp simpleTopicPartition) GetPartition() int32 { return tp.partition }
func (tp simpleTopicPartition) GetOffset() int64    { return tp.offset }
func (tp simpleTopicPartition) GetError() error     { return nil }

type simpleMessage struct {
	tp      simpleTopicPartition
	headers []Header
}

func (m *simpleMessage) GetTopicPartition() TopicPartition { return m.tp }
func (m *simpleMessage) GetHeaders() []Header              { return m.headers }
func (m *simpleMessage) SetHeaders(h []Header)             { m.headers = h }
func (m *simpleMessage) GetValue() []byte                  { return nil }
func (m *simpleMessage) GetKey() []byte                    { return nil }
func (m *simpleMessage) Unwrap() any                       { return m }

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

	tr := NewKafkaTracer(testInstr, CKGoVersion1, 0)
	msg := &simpleMessage{tp: simpleTopicPartition{topic: "test-topic", partition: 0, offset: 0}}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		span := tr.StartProduceSpan(msg)
		span.Finish()
	}
}
