// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package shopify builds github.com/Shopify/sarama producers and consumers on behalf of
// its caller.
package shopify

import "github.com/Shopify/sarama"

func NewSyncProducer(addrs []string, cfg *sarama.Config) (sarama.SyncProducer, error) {
	return sarama.NewSyncProducer(addrs, cfg)
}

func NewAsyncProducer(addrs []string, cfg *sarama.Config) (sarama.AsyncProducer, error) {
	return sarama.NewAsyncProducer(addrs, cfg)
}

func NewConsumer(addrs []string, cfg *sarama.Config) (sarama.Consumer, error) {
	return sarama.NewConsumer(addrs, cfg)
}
