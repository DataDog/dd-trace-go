package pubsub_test

import (
	"context"
	"log"

	ddpubsub "gopkg.in/DataDog/dd-trace-go.v1/contrib/cloud.google.com/go/pubsub.v1"

	"cloud.google.com/go/pubsub"
)

func ExamplePublish() {
	client, err := pubsub.NewClient(context.Background(), "project-id")
	if err != nil {
		log.Fatal(err)
	}

	topic := client.Topic("topic")
	_, err = ddpubsub.Publish(context.Background(), topic, &pubsub.Message{Data: []byte("hello world!")})
	if err != nil {
		log.Fatal(err)
	}
}

func ExampleReceive() {
	client, err := pubsub.NewClient(context.Background(), "project-id")
	if err != nil {
		log.Fatal(err)
	}

	sub := client.Subscription("subscription")
	err = sub.Receive(context.Background(), ddpubsub.ReceiveTracer(sub, func(ctx context.Context, msg *pubsub.Message) {
		// TODO: Handle message.
	}))
	if err != nil {
		log.Fatal(err)
	}
}
