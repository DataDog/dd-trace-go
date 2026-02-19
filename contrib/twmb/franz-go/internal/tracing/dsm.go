// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracing

import (
	"context"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func (tr *Tracer) SetConsumeDSMCheckpoint(r Record) {
	if !tr.dataStreamsEnabled || r == nil {
		return
	}
	edges := []string{"direction:in", "topic:" + r.GetTopic(), "type:kafka"}
	if tr.kafkaCfg.ConsumerGroupID != "" {
		edges = append(edges, "group:"+tr.kafkaCfg.ConsumerGroupID)
	}
	carrier := NewKafkaHeadersCarrier(r)
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		datastreams.ExtractFromBase64Carrier(context.Background(), carrier),
		options.CheckpointParams{PayloadSize: getConsumerMsgSize(r)},
		edges...,
	)
	if !ok {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
	if tr.kafkaCfg.ConsumerGroupID != "" {
		// Only track Kafka lag if a consumer group is set.
		// Since there is no ack mechanism, we consider that messages read are committed right away.
		tracer.TrackKafkaCommitOffset(tr.kafkaCfg.ConsumerGroupID, r.GetTopic(), int32(r.GetPartition()), r.GetOffset())
	}
}

func (tr *Tracer) SetProduceDSMCheckpoint(r Record) {
	if !tr.dataStreamsEnabled || r == nil {
		return
	}

	topic := r.GetTopic()

	edges := []string{"direction:out", "topic:" + topic, "type:kafka"}
	carrier := NewKafkaHeadersCarrier(r)
	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(
		datastreams.ExtractFromBase64Carrier(context.Background(), carrier),
		options.CheckpointParams{PayloadSize: getProducerMsgSize(r)},
		edges...,
	)
	if !ok {
		return
	}

	// Headers will be dropped if the current protocol does not support them
	datastreams.InjectToBase64Carrier(ctx, carrier)
}

func getProducerMsgSize(r Record) (size int64) {
	for _, header := range r.GetHeaders() {
		size += int64(len(header.GetKey()) + len(header.GetValue()))
	}
	if r.GetValue() != nil {
		size += int64(len(r.GetValue()))
	}
	if r.GetKey() != nil {
		size += int64(len(r.GetKey()))
	}
	return size
}

func getConsumerMsgSize(r Record) (size int64) {
	for _, header := range r.GetHeaders() {
		size += int64(len(header.GetKey()) + len(header.GetValue()))
	}
	return size + int64(len(r.GetValue())+len(r.GetKey()))
}
