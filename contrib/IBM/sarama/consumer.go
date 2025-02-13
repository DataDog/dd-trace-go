// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sarama

import (
	"context"

	"github.com/IBM/sarama"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/datastreams/options"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

type partitionConsumer struct {
	sarama.PartitionConsumer
	dispatcher dispatcher
}

// Messages returns the read channel for the messages that are returned by
// the broker.
func (pc *partitionConsumer) Messages() <-chan *sarama.ConsumerMessage {
	return pc.dispatcher.Messages()
}

// WrapPartitionConsumer wraps a sarama.PartitionConsumer causing each received
// message to be traced.
func WrapPartitionConsumer(pc sarama.PartitionConsumer, opts ...Option) sarama.PartitionConsumer {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	instr.Logger().Debug("contrib/IBM/sarama: Wrapping Partition Consumer: %#v", cfg)

	d := wrapDispatcher(pc, cfg)
	go d.Run()

	wrapped := &partitionConsumer{
		PartitionConsumer: pc,
		dispatcher:        d,
	}
	return wrapped
}

type consumer struct {
	sarama.Consumer
	opts []Option
}

// ConsumePartition invokes Consumer.ConsumePartition and wraps the resulting
// PartitionConsumer.
func (c *consumer) ConsumePartition(topic string, partition int32, offset int64) (sarama.PartitionConsumer, error) {
	pc, err := c.Consumer.ConsumePartition(topic, partition, offset)
	if err != nil {
		return pc, err
	}
	return WrapPartitionConsumer(pc, c.opts...), nil
}

// WrapConsumer wraps a sarama.Consumer wrapping any PartitionConsumer created
// via Consumer.ConsumePartition.
func WrapConsumer(c sarama.Consumer, opts ...Option) sarama.Consumer {
	return &consumer{
		Consumer: c,
		opts:     opts,
	}
}

func setConsumeCheckpoint(enabled bool, groupID string, msg *sarama.ConsumerMessage) {
	if !enabled || msg == nil {
		return
	}
	edges := []string{"direction:in", "topic:" + msg.Topic, "type:kafka"}
	if groupID != "" {
		edges = append(edges, "group:"+groupID)
	}
	carrier := NewConsumerMessageCarrier(msg)

	ctx, ok := tracer.SetDataStreamsCheckpointWithParams(datastreams.ExtractFromBase64Carrier(context.Background(), carrier), options.CheckpointParams{PayloadSize: getConsumerMsgSize(msg)}, edges...)
	if !ok {
		return
	}
	datastreams.InjectToBase64Carrier(ctx, carrier)
	if groupID != "" {
		// only track Kafka lag if a consumer group is set.
		// since there is no ack mechanism, we consider that messages read are committed right away.
		tracer.TrackKafkaCommitOffset(groupID, msg.Topic, msg.Partition, msg.Offset)
	}
}

func getConsumerMsgSize(msg *sarama.ConsumerMessage) (size int64) {
	for _, header := range msg.Headers {
		size += int64(len(header.Key) + len(header.Value))
	}
	return size + int64(len(msg.Value)+len(msg.Key))
}
