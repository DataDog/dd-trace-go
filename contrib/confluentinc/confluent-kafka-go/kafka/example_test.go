// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka_test

import (
	"fmt"

	"github.com/confluentinc/confluent-kafka-go/kafka"

	kafkatrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/kafka"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var (
	testGroupID = "gotest"
	testTopic   = "gotest"
)

// This example shows how a span context can be passed from a producer to a consumer.
func ExampleDistributedTracing() {

	tracer.Start()
	defer tracer.Stop()

	c, err := kafkatrace.NewConsumer(&kafka.ConfigMap{
		"go.events.channel.enable": true, // required for the events channel to be turned on
		"group.id":                 testGroupID,
		"socket.timeout.ms":        10,
		"session.timeout.ms":       10,
		"enable.auto.offset.store": false,
	})

	err = c.Subscribe(testTopic, nil)
	if err != nil {
		panic(err)
	}

	// Create the span to be passed
	parentSpan := tracer.StartSpan("test_parent_span")

	/// Produce a message with a span
	go func() {
		msg := &kafka.Message{
			TopicPartition: kafka.TopicPartition{
				Topic:     &testTopic,
				Partition: 1,
				Offset:    1,
			},
			Key:   []byte("key1"),
			Value: []byte("value1"),
		}

		// Inject the span context in the message to be produced
		carrier := kafkatrace.NewMessageCarrier(msg)
		tracer.Inject(parentSpan.Context(), carrier)

		c.Consumer.Events() <- msg

	}()

	msg := (<-c.Events()).(*kafka.Message)

	// Extract the context from the message
	carrier := kafkatrace.NewMessageCarrier(msg)
	spanContext, err := tracer.Extract(carrier)
	if err != nil {
		panic(err)
	}

	parentContext := parentSpan.Context()

	// Validate that the context passed is the context sent via the message
	if spanContext.TraceID() == parentContext.TraceID() {
		fmt.Println("Span context passed sucessfully from producer to consumer")
	} else {
		fmt.Println("Span context not passed")
	}

	c.Close()
	// wait for the events channel to be closed
	<-c.Events()
	// Output: Span context passed sucessfully from producer to consumer
}
