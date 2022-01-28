package kafka_test

import (
	"context"
	"log"
	"time"

	kafkatrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/segmentio/kafka.go.v0"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	kafka "github.com/segmentio/kafka-go"
)

func ExampleWriter() {
	w := kafkatrace.NewWriter(kafka.WriterConfig{
		Brokers: []string{"localhost:9092"},
		Topic:   "some-topic",
	})

	// use slice as it passes the value by reference if you want message headers updated in kafkatrace
	msgs := []kafka.Message{
		{
			Key:   []byte("key1"),
			Value: []byte("value1"),
		},
	}
	if err := w.WriteMessages(context.Background(), msgs...); err != nil {
		log.Fatal("Failed to write message", err)
	}
}

func ExampleReader() {
	r := kafkatrace.NewReader(kafka.ReaderConfig{
		Brokers:        []string{"localhost:9092"},
		Topic:          "some-topic",
		GroupID:        "group-id",
		SessionTimeout: 30 * time.Second,
	})
	msg, err := r.ReadMessage(context.Background())
	if err != nil {
		log.Fatal("Failed to read message", err)
	}

	// create a child span using span id and trace id in message header
	spanContext, err := kafkatrace.ExtractSpanContextFromMessage(msg)
	if err != nil {
		log.Fatal("Failed to extract span context from carrier", err)
	}
	operationName := "child-span"
	s := tracer.StartSpan(operationName, tracer.ChildOf(spanContext))
	defer s.Finish()
}
