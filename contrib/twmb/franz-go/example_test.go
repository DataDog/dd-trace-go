// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kgo_test

import (
	"context"
	"log"

	kgotrace "github.com/DataDog/dd-trace-go/contrib/twmb/franz-go/v2"
	"github.com/DataDog/dd-trace-go/contrib/twmb/franz-go/v2/internal/tracing"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"github.com/twmb/franz-go/pkg/kgo"
)

func Example() {
	// Create a traced client with default configuration
	client, err := kgotrace.NewClient(
		kgotrace.ClientOptions(
			kgo.SeedBrokers("localhost:9092"),
			kgo.ConsumeTopics("my-topic"),
		),
	)
	if err != nil {
		log.Fatal("Failed to create client:", err)
	}
	defer client.Close()

	// Produce a message - tracing is automatic
	ctx := context.Background()
	record := &kgo.Record{
		Topic: "my-topic",
		Value: []byte("Hello, Kafka!"),
	}
	if err := client.ProduceSync(ctx, record).FirstErr(); err != nil {
		log.Fatal("Failed to produce:", err)
	}

	// Consume messages - tracing is automatic
	fetches := client.PollFetches(ctx)
	fetches.EachRecord(func(r *kgo.Record) {
		log.Printf("Consumed: %s", string(r.Value))
	})
}

func Example_withTracingOptions() {
	// Create a traced client with custom tracing options
	client, err := kgotrace.NewClient(
		kgotrace.ClientOptions(
			kgo.SeedBrokers("localhost:9092"),
			kgo.ConsumeTopics("my-topic"),
			kgo.ConsumerGroup("my-consumer-group"),
		),
		tracing.WithService("my-service"),
		tracing.WithAnalytics(true),
		tracing.WithDataStreams(),
	)
	if err != nil {
		log.Fatal("Failed to create client:", err)
	}
	defer client.Close()

	// Use the client normally - tracing options are applied automatically
	ctx := context.Background()
	record := &kgo.Record{
		Topic: "my-topic",
		Value: []byte("Hello, Kafka!"),
	}
	if err := client.ProduceSync(ctx, record).FirstErr(); err != nil {
		log.Fatal("Failed to produce:", err)
	}
}

func Example_manualChildSpan() {
	// Create a traced client
	client, err := kgotrace.NewClient(
		kgotrace.ClientOptions(
			kgo.SeedBrokers("localhost:9092"),
			kgo.ConsumeTopics("my-topic"),
		),
	)
	if err != nil {
		log.Fatal("Failed to create client:", err)
	}
	defer client.Close()

	// Consume a message
	ctx := context.Background()
	fetches := client.PollFetches(ctx)

	fetches.EachRecord(func(r *kgo.Record) {
		// Extract the span context from the consumed message
		spanContext, err := kgotrace.ExtractSpanContext(r)
		if err != nil {
			log.Fatal("Failed to extract span context:", err)
		}

		// Create a child span for processing the message
		span := tracer.StartSpan("process-message", tracer.ChildOf(spanContext))
		defer span.Finish()

		// Process the message with the child span
		log.Printf("Processing: %s", string(r.Value))
	})
}
