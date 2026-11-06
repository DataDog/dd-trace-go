// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

// Command mainapp calls the confluent-kafka-go clients from package main, which
// the harness suites never build. TestClientsInMain builds and runs it.
package main

import (
	"fmt"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
)

const topic = "confluent_kafka_v2_main_test"

func main() {
	cluster, err := kafka.NewMockCluster(1)
	if err != nil {
		panic(err)
	}
	defer cluster.Close()
	servers := cluster.BootstrapServers()

	p, err := kafka.NewProducer(&kafka.ConfigMap{"bootstrap.servers": servers})
	if err != nil {
		panic(err)
	}
	produce(p, "main")
	produceAliased(servers, "alias")

	c, err := kafka.NewConsumer(&kafka.ConfigMap{"bootstrap.servers": servers, "group.id": "main"})
	if err != nil {
		panic(err)
	}
	consume(c, 2)
	consumeAliased(servers, 2)

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func message(key string) *kafka.Message {
	t := topic
	return &kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &t, Partition: 0},
		Key:            []byte(key),
	}
}

func produce(p *kafka.Producer, key string) {
	delivery := make(chan kafka.Event, 1)
	if err := p.Produce(message(key), delivery); err != nil {
		panic(err)
	}
	if msg := (<-delivery).(*kafka.Message); msg.TopicPartition.Error != nil {
		panic(msg.TopicPartition.Error)
	}
	p.Close()
}

func consume(c *kafka.Consumer, n int) {
	t := topic
	if err := c.Assign([]kafka.TopicPartition{{Topic: &t, Partition: 0, Offset: kafka.OffsetBeginning}}); err != nil {
		panic(err)
	}
	for n > 0 {
		if _, err := c.ReadMessage(10 * time.Second); err != nil {
			panic(err)
		}
		n--
	}
	if err := c.Close(); err != nil {
		panic(err)
	}
}
