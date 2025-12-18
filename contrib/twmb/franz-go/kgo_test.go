// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kgo

import (
	"context"
	"errors"
	"log"
	"os"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	testGroupID       = "kgo-test-group-id"
	testTopic         = "kgo-test-topic"
	testReaderMaxWait = 10 * time.Millisecond
)

var (
	// Add dummy values to broker/addr to test bootstrap servers
	seedBrokers = []string{"localhost:9092", "localhost:9093", "localhost:9094"}
)

// NOTE: TestMain is executed first before the tests
// Do the setup, checks if you actually need to run the integration tests
func TestMain(m *testing.M) {
	// _, ok := os.LookupEnv("INTEGRATION")
	// if !ok {
	// 	log.Println("🚧 Skipping integration test (INTEGRATION environment variable is not set)")
	// 	os.Exit(0)
	// }
	cleanup := createTopic()
	exitCode := m.Run()
	cleanup()
	os.Exit(exitCode)
}

func createTopic() func() {
	// One client can both produce and consume!
	// Consuming can either be direct (no consumer group), or through a group. Below, we use a group.
	cl, err := kgo.NewClient(kgo.SeedBrokers(seedBrokers...))
	if err != nil {
		log.Fatalf("failed to create client: %v", err)
	}

	admCl := kadm.NewClient(cl)

	ctx := context.Background()
	_, err = admCl.DeleteTopics(ctx, testTopic)
	if err != nil && !errors.Is(err, kerr.UnknownTopicOrPartition) {
		log.Fatalf("failed to delete topic: %v", err)
	}

	_, err = admCl.CreateTopic(ctx, 1, 1, nil, testTopic)
	if err != nil {
		log.Fatalf("failed to create topic: %v", err)
	}

	if err := ensureTopicReady(); err != nil {
		log.Fatalf("failed to ensure topic is ready: %v", err)
	}

	return func() {
		defer admCl.Close()
		defer cl.Close()

		_, err = admCl.DeleteTopics(context.Background(), testTopic)
		if err != nil {
			log.Printf("failed to delete topic during cleanup: %v", err)
		}
	}
}

func ensureTopicReady() error {
	const (
		maxRetries = 10
		retryDelay = 100 * time.Millisecond
	)

	cl, err := kgo.NewClient(kgo.SeedBrokers(seedBrokers...))
	if err != nil {
		return err
	}
	defer cl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var retryCount int
	for retryCount < maxRetries {
		// Try to produce a test message
		record := &kgo.Record{
			Topic: testTopic,
			Key:   []byte("test-key"),
			Value: []byte("test-value"),
		}

		results := cl.ProduceSync(ctx, record)
		err = results.FirstErr()
		if err == nil {
			break
		}

		// This error happens sometimes with brand-new topics, as there is a delay between when the topic is created
		// on the broker, and when the topic can actually be written to.
		if errors.Is(err, kerr.UnknownTopicOrPartition) {
			retryCount++
			log.Printf("topic not ready yet, retrying produce in %s (retryCount: %d)\n", retryDelay, retryCount)
			time.Sleep(retryDelay)
			continue
		}
		return err
	}
	if err != nil {
		return err
	}

	// Consume the test message to clean up
	cl.AddConsumeTopics(testTopic)
	fetches := cl.PollFetches(ctx)
	if fetches.IsClientClosed() {
		return errors.New("client closed while polling")
	}

	return fetches.Err()
}

func TestTest(t *testing.T) {
	log.Println("test")
}
