// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp writes and reads Kafka messages from package main, which
// does not import the contrib. TestMessagesInMain builds and runs it against
// the broker in KAFKA_ADDR and the topic in KAFKA_TOPIC.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/segmentio/kafka-go"
)

func main() {
	//nolint:forbidigo // The app does not import dd-trace-go.
	addr, topic := os.Getenv("KAFKA_ADDR"), os.Getenv("KAFKA_TOPIC")
	ctx := context.Background()

	writer := &kafka.Writer{Addr: kafka.TCP(addr), Topic: topic}
	err := writer.WriteMessages(ctx,
		kafka.Message{Value: []byte("first")},
		kafka.Message{Value: []byte("second")},
	)
	if err != nil {
		panic(err)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: []string{addr},
		Topic:   topic,
		MaxWait: 10 * time.Millisecond,
	})
	for range 2 {
		if _, err := reader.ReadMessage(ctx); err != nil {
			panic(err)
		}
	}
	if err := reader.Close(); err != nil {
		panic(err)
	}

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}
