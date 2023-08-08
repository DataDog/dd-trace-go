// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/confluentinc/confluent-kafka-go/kafka"
)

func TraceKafkaProduce(ctx context.Context, msg *kafka.Message) context.Context {
	edges := []string{"direction:out"}
	if msg.TopicPartition.Topic != nil {
		edges = append(edges, "topic:"+*msg.TopicPartition.Topic)
	}
	edges = append(edges, "type:kafka")
	p, ctx, ok := tracer.SetDataStreamsCheckpoint(ctx, edges...)
	if ok {
		msg.Headers = append(msg.Headers, kafka.Header{Key: datastreams.PropagationKey, Value: p.Encode()})
	}
	return ctx
}

func TraceKafkaConsume(ctx context.Context, msg *kafka.Message, group string) context.Context {
	for _, header := range msg.Headers {
		if header.Key == datastreams.PropagationKey {
			_, ctx, _ = datastreams.Decode(ctx, header.Value)
			break
		}
	}
	edges := []string{"direction:in", "group:" + group}
	if msg.TopicPartition.Topic != nil {
		edges = append(edges, "topic:"+*msg.TopicPartition.Topic)
	}
	edges = append(edges, "type:kafka")
	edges = append(edges)
	_, ctx, _ = tracer.SetDataStreamsCheckpoint(ctx, edges...)
	return ctx
}
