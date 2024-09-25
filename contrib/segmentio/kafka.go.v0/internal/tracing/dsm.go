// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracing

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func SetConsumeDSMCheckpoint(cfg *Config, kafkaCfg *KafkaConfig, msg *KafkaMessage) {
	if !cfg.dataStreamsEnabled || msg == nil {
		return
	}
	edges := []string{"direction:in", "topic:" + msg.Topic, "type:kafka"}
	if kafkaCfg.ConsumerGroupID != "" {
		edges = append(edges, "group:"+kafkaCfg.ConsumerGroupID)
	}
	carrier := MessageCarrier{msg}
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		datastreams.ExtractFromBase64Carrier(context.Background(), carrier),
		options.CheckpointParams{PayloadSize: getConsumerMsgSize(msg)},
		edges...,
	)
	if !ok {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
	if kafkaCfg.ConsumerGroupID != "" {
		// only track Kafka lag if a consumer group is set.
		// since there is no ack mechanism, we consider that messages read are committed right away.
		tracer.TrackKafkaCommitOffset(kafkaCfg.ConsumerGroupID, msg.Topic, int32(msg.Partition), msg.Offset)
	}
}

func SetProduceDSMCheckpoint(cfg *Config, msg *KafkaMessage, writer *KafkaWriter) {
	if !cfg.dataStreamsEnabled || msg == nil {
		return
	}

	var topic string
	if writer.Topic != "" {
		topic = writer.Topic
	} else {
		topic = msg.Topic
	}

	edges := []string{"direction:out", "topic:" + topic, "type:kafka"}
	carrier := MessageCarrier{msg}
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		datastreams.ExtractFromBase64Carrier(context.Background(), carrier),
		options.CheckpointParams{PayloadSize: getProducerMsgSize(msg)},
		edges...,
	)
	if !ok {
		return
	}

	// Headers will be dropped if the current protocol does not support them
	datastreams.InjectToBase64Carrier(ctx, carrier)
}

func getProducerMsgSize(msg *KafkaMessage) (size int64) {
	for _, header := range msg.Headers {
		size += int64(len(header.Key) + len(header.Value))
	}
	if msg.Value != nil {
		size += int64(len(msg.Value))
	}
	if msg.Key != nil {
		size += int64(len(msg.Key))
	}
	return size
}

func getConsumerMsgSize(msg *KafkaMessage) (size int64) {
	for _, header := range msg.Headers {
		size += int64(len(header.Key) + len(header.Value))
	}
	return size + int64(len(msg.Value)+len(msg.Key))
}
