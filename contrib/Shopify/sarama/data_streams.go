// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/Shopify/sarama"
)

func TraceKafkaProduce(ctx context.Context, msg *sarama.ProducerMessage) context.Context {
	edges := []string{"direction:out", "topic:" + msg.Topic, "type:kafka"}
	p, ctx, ok := tracer.SetDataStreamsCheckpoint(ctx, edges...)
	if ok {
		msg.Headers = append(msg.Headers, sarama.RecordHeader{Key: []byte(datastreams.PropagationKey), Value: p.Encode()})
	}
	return ctx
}

func TraceKafkaConsume(ctx context.Context, msg *sarama.ConsumerMessage, group string) context.Context {
	for _, header := range msg.Headers {
		if header != nil && string(header.Key) == datastreams.PropagationKey {
			_, ctx, _ = datastreams.Decode(ctx, header.Value)
			break
		}
	}
	edges := []string{"direction:in", "group:" + group, "topic:" + msg.Topic, "type:kafka"}
	_, ctx, _ = tracer.SetDataStreamsCheckpoint(ctx, edges...)
	return ctx
}
