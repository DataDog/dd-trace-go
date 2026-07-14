// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package pubsub_test

import (
	"context"
	"log"

	"cloud.google.com/go/pubsub/v2"

	pubsubtrace "github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v2/v2"
)

func ExamplePublish() {
	client, err := pubsub.NewClient(context.Background(), "project-id")
	if err != nil {
		log.Fatal(err)
	}

	publisher := client.Publisher("topic")
	_, err = pubsubtrace.Publish(context.Background(), publisher, &pubsub.Message{Data: []byte("hello world!")}).Get(context.Background())
	if err != nil {
		log.Fatal(err)
	}
}

func ExampleSubscriber_Receive() {
	client, err := pubsub.NewClient(context.Background(), "project-id")
	if err != nil {
		log.Fatal(err)
	}

	sub := client.Subscriber("subscription")
	err = sub.Receive(context.Background(), pubsubtrace.WrapReceiveHandler(sub, func(_ context.Context, _ *pubsub.Message) {
		// TODO: Handle message.
	}))
	if err != nil {
		log.Fatal(err)
	}
}
