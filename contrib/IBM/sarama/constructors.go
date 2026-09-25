// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package sarama

import "github.com/IBM/sarama"

// NewConsumer calls sarama.NewConsumer and wraps the resulting consumer.
func NewConsumer(addrs []string, cfg *sarama.Config) (sarama.Consumer, error) {
	consumer, err := sarama.NewConsumer(addrs, cfg)
	if err != nil {
		return nil, err
	}
	return WrapConsumer(consumer, WithBrokers(addrs), withSaramaConfig(cfg)), nil
}

// NewConsumerFromClient calls sarama.NewConsumerFromClient and wraps the resulting consumer.
func NewConsumerFromClient(client sarama.Client) (sarama.Consumer, error) {
	consumer, err := sarama.NewConsumerFromClient(client)
	if err != nil {
		return nil, err
	}
	return WrapConsumer(consumer, WithBrokers(clientBrokerAddresses(client)), withSaramaConfig(client.Config())), nil
}

// NewSyncProducer calls sarama.NewSyncProducer and wraps the resulting producer.
func NewSyncProducer(addrs []string, cfg *sarama.Config) (sarama.SyncProducer, error) {
	producer, err := sarama.NewSyncProducer(addrs, cfg)
	if err != nil {
		return nil, err
	}
	return WrapSyncProducer(cfg, producer, WithBrokers(addrs)), nil
}

// NewSyncProducerFromClient calls sarama.NewSyncProducerFromClient and wraps the resulting producer.
func NewSyncProducerFromClient(client sarama.Client) (sarama.SyncProducer, error) {
	producer, err := sarama.NewSyncProducerFromClient(client)
	if err != nil {
		return nil, err
	}
	return WrapSyncProducer(client.Config(), producer, WithBrokers(clientBrokerAddresses(client))), nil
}

// NewAsyncProducer calls sarama.NewAsyncProducer and wraps the resulting producer.
func NewAsyncProducer(addrs []string, cfg *sarama.Config) (sarama.AsyncProducer, error) {
	producer, err := sarama.NewAsyncProducer(addrs, cfg)
	if err != nil {
		return nil, err
	}
	return WrapAsyncProducer(cfg, producer, WithBrokers(addrs)), nil
}

// NewAsyncProducerFromClient calls sarama.NewAsyncProducerFromClient and wraps the resulting producer.
func NewAsyncProducerFromClient(client sarama.Client) (sarama.AsyncProducer, error) {
	producer, err := sarama.NewAsyncProducerFromClient(client)
	if err != nil {
		return nil, err
	}
	return WrapAsyncProducer(client.Config(), producer, WithBrokers(clientBrokerAddresses(client))), nil
}

func clientBrokerAddresses(client sarama.Client) []string {
	brokers := client.Brokers()
	addrs := make([]string, 0, len(brokers))
	for _, broker := range brokers {
		addrs = append(addrs, broker.Addr())
	}
	return addrs
}

func withSaramaConfig(saramaConfig *sarama.Config) OptionFn {
	return func(cfg *config) {
		cfg.saramaConfig = saramaConfig
	}
}
