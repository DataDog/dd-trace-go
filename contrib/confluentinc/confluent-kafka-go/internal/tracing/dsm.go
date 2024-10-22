// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracing

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/datastreams"
	"gopkg.in/DataDog/dd-trace-go.v1/datastreams/options"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func (tr *KafkaTracer) TrackCommitOffsets(offsets []TopicPartition, err error) {
	if err != nil || tr.groupID == "" || !tr.dsmEnabled {
		return
	}
	for _, tp := range offsets {
		tracer.TrackKafkaCommitOffset(tr.groupID, tp.GetTopic(), tp.GetPartition(), tp.GetOffset())
	}
}

func (tr *KafkaTracer) TrackHighWatermarkOffset(offsets []TopicPartition, consumer Consumer) {
	if !tr.dsmEnabled {
		return
	}
	for _, tp := range offsets {
		if _, high, err := consumer.GetWatermarkOffsets(tp.GetTopic(), tp.GetPartition()); err == nil {
			tracer.TrackKafkaHighWatermarkOffset("", tp.GetTopic(), tp.GetPartition(), high)
		}
	}
}

func (tr *KafkaTracer) TrackProduceOffsets(msg Message) {
	err := msg.GetTopicPartition().GetError()
	if err != nil || !tr.dsmEnabled || msg.GetTopicPartition().GetTopic() == "" {
		return
	}
	tp := msg.GetTopicPartition()
	tracer.TrackKafkaProduceOffset(tp.GetTopic(), tp.GetPartition(), tp.GetOffset())
}

func (tr *KafkaTracer) SetConsumeCheckpoint(msg Message) {
	if !tr.dsmEnabled || msg == nil {
		return
	}
	edges := []string{"direction:in", "topic:" + msg.GetTopicPartition().GetTopic(), "type:kafka"}
	if tr.groupID != "" {
		edges = append(edges, "group:"+tr.groupID)
	}
	carrier := NewMessageCarrier(msg)
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		datastreams.ExtractFromBase64Carrier(context.Background(), carrier),
		options.CheckpointParams{PayloadSize: getMsgSize(msg)},
		edges...,
	)
	if !ok {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
}

func (tr *KafkaTracer) SetProduceCheckpoint(msg Message) {
	if !tr.dsmEnabled || msg == nil {
		return
	}
	edges := []string{"direction:out", "topic:" + msg.GetTopicPartition().GetTopic(), "type:kafka"}
	carrier := NewMessageCarrier(msg)
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		datastreams.ExtractFromBase64Carrier(context.Background(), carrier),
		options.CheckpointParams{PayloadSize: getMsgSize(msg)},
		edges...,
	)
	if !ok || tr.librdKafkaVersion < 0x000b0400 {
		// headers not supported before librdkafka >=0.11.4
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
}

func getMsgSize(msg Message) (size int64) {
	for _, header := range msg.GetHeaders() {
		size += int64(len(header.GetKey()) + len(header.GetValue()))
	}
	return size + int64(len(msg.GetValue())+len(msg.GetKey()))
}
