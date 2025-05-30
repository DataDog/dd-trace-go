// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package sarama provides functions to trace the Shopify/sarama package (https://github.com/Shopify/sarama).
//
// Deprecated: github.com/Shopify/sarama is no longer maintained. Please migrate to github.com/IBM/sarama
// and use the corresponding instrumentation.
package sarama // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/Shopify/sarama"

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/Shopify/sarama/v2"

	"github.com/Shopify/sarama"
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
