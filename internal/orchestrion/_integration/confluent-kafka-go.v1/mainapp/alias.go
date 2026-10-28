// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package main

// Imported under another name than kafka, which the rules must handle the way
// orchestrion does.
import (
	ckafka "github.com/confluentinc/confluent-kafka-go/kafka"
)

func produceAliased(servers, key string) {
	p, err := ckafka.NewProducer(&ckafka.ConfigMap{"bootstrap.servers": servers})
	if err != nil {
		panic(err)
	}
	produce(p, key)
}

func consumeAliased(servers string, n int) {
	c, err := ckafka.NewConsumer(&ckafka.ConfigMap{"bootstrap.servers": servers, "group.id": "alias"})
	if err != nil {
		panic(err)
	}
	consume(c, n)
}
