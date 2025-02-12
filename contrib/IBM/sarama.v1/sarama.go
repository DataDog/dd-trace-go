// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package sarama provides functions to trace the IBM/sarama package (https://github.com/IBM/sarama).
package sarama // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/IBM/sarama"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/IBM/sarama/v2"

	"github.com/IBM/sarama"
)

// WrapPartitionConsumer wraps a sarama.PartitionConsumer causing each received
// message to be traced.
func WrapPartitionConsumer(pc sarama.PartitionConsumer, opts ...Option) sarama.PartitionConsumer {
	return v2.WrapPartitionConsumer(pc, opts...)
}

// WrapConsumer wraps a sarama.Consumer wrapping any PartitionConsumer created
// via Consumer.ConsumePartition.
func WrapConsumer(c sarama.Consumer, opts ...Option) sarama.Consumer {
	return v2.WrapConsumer(c, opts...)
}

// WrapSyncProducer wraps a sarama.SyncProducer so that all produced messages
// are traced.
func WrapSyncProducer(saramaConfig *sarama.Config, producer sarama.SyncProducer, opts ...Option) sarama.SyncProducer {
	return v2.WrapSyncProducer(saramaConfig, producer, opts...)
}

// WrapAsyncProducer wraps a sarama.AsyncProducer so that all produced messages
// are traced. It requires the underlying sarama Config so we can know whether
// or not successes will be returned. Tracing requires at least sarama.V0_11_0_0
// version which is the first version that supports headers. Only spans of
// successfully published messages have partition and offset tags set.
func WrapAsyncProducer(saramaConfig *sarama.Config, p sarama.AsyncProducer, opts ...Option) sarama.AsyncProducer {
	return v2.WrapAsyncProducer(saramaConfig, p, opts...)
}

// WrapConsumerGroupHandler wraps a sarama.ConsumerGroupHandler causing each received
// message to be traced.
func WrapConsumerGroupHandler(handler sarama.ConsumerGroupHandler, opts ...Option) sarama.ConsumerGroupHandler {
	return v2.WrapConsumerGroupHandler(handler, opts...)
}
