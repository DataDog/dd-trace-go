// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package amqp091_test

import (
	"context"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"

	amqptrace "github.com/DataDog/dd-trace-go/contrib/rabbitmq/amqp091-go/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func Example() {
	tracer.Start()
	defer tracer.Stop()

	conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	if err := amqptrace.PublishWithContext(ctx, ch, "", "orders", false, false, amqp.Publishing{Body: []byte("hello")}); err != nil {
		log.Fatal(err)
	}

	deliveries, err := amqptrace.Consume(ch, "orders", "", true, false, false, false, nil)
	if err != nil {
		log.Fatal(err)
	}
	for d := range deliveries {
		ctx := amqptrace.ContextFromDelivery(ctx, d)
		if err := amqptrace.PublishWithContext(ctx, ch, "", "orders-processed", false, false, amqp.Publishing{Body: d.Body}); err != nil {
			log.Print(err)
		}
	}
}
