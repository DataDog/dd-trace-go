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

func (tr *Tracer) SetConsumeDSMCheckpoint(msg Message) {
	if !tr.dataStreamsEnabled || msg == nil {
		return
	}
	edges := []string{"direction:in", "topic:" + msg.GetTopic(), "type:kafka"}
	if tr.kafkaCfg.ConsumerGroupID != "" {
		edges = append(edges, "group:"+tr.kafkaCfg.ConsumerGroupID)
	}
	carrier := NewMessageCarrier(msg)
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		datastreams.ExtractFromBase64Carrier(context.Background(), carrier),
		options.CheckpointParams{PayloadSize: getConsumerMsgSize(msg)},
		edges...,
	)
	if !ok {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
	if tr.kafkaCfg.ConsumerGroupID != "" {
		// only track Kafka lag if a consumer group is set.
		// since there is no ack mechanism, we consider that messages read are committed right away.
		tracer.TrackKafkaCommitOffset(tr.kafkaCfg.ConsumerGroupID, msg.GetTopic(), int32(msg.GetPartition()), msg.GetOffset())
	}
}

func (tr *Tracer) SetProduceDSMCheckpoint(msg Message, writer Writer) {
	if !tr.dataStreamsEnabled || msg == nil {
		return
	}

	var topic string
	if writer.GetTopic() != "" {
		topic = writer.GetTopic()
	} else {
		topic = msg.GetTopic()
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

func getProducerMsgSize(msg Message) (size int64) {
	for _, header := range msg.GetHeaders() {
		size += int64(len(header.GetKey()) + len(header.GetValue()))
	}
	if msg.GetValue() != nil {
		size += int64(len(msg.GetValue()))
	}
	if msg.GetKey() != nil {
		size += int64(len(msg.GetKey()))
	}
	return size
}

func getConsumerMsgSize(msg Message) (size int64) {
	for _, header := range msg.GetHeaders() {
		size += int64(len(header.GetKey()) + len(header.GetValue()))
	}
	return size + int64(len(msg.GetValue())+len(msg.GetKey()))
}
