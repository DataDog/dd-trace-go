// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package franz_test

import (
	"context"
	"log"

	"github.com/twmb/franz-go/pkg/kgo"
	franztrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/twmb/franz-go/franz"
)

func Example_producer() {
	// Create a new client with tracing enabled
	client, err := franztrace.NewClient([]kgo.Opt{
		kgo.SeedBrokers("localhost:9092"),
	})
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Create a record to produce
	record := &kgo.Record{
		Topic: "some-topic",
		Value: []byte("Hello World"),
	}

	// Produce the record with tracing
	client.Produce(context.Background(), record, func(r *kgo.Record, err error) {
		if err != nil {
			log.Printf("Failed to produce record: %v", err)
		}
	})
}

func Example_consumer() {
	// Create a new client with tracing enabled
	client, err := franztrace.NewClient([]kgo.Opt{
		kgo.SeedBrokers("localhost:9092"),
		kgo.ConsumerGroup("my-group"),
		kgo.ConsumeTopics("some-topic"),
	}, franztrace.WithGroupID("my-group"))
	if err != nil {
		panic(err)
	}
	defer client.Close()

	// Poll for records
	ctx := context.Background()
	for {
		fetches := client.PollFetches(ctx)
		if errs := fetches.Errors(); len(errs) > 0 {
			// Handle errors
			for _, err := range errs {
				log.Printf("Error polling: %v", err)
			}
			continue
		}

		// Iterate through records
		iter := fetches.RecordIter()
		for !iter.Done() {
			record := iter.Next()
			log.Printf("Consumed record from topic %s partition %d at offset %d",
				record.Topic, record.Partition, record.Offset)
		}
	}
}
