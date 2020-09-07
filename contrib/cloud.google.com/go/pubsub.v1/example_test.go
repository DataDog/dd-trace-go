package pubsub

import (
	"context"

	"cloud.google.com/go/pubsub"
)

func ExamplePublish() {
	ctx := context.Background()
	client, err := pubsub.NewClient(ctx, "project-id")
	if err != nil {
		// TODO: Handle error.
	}

	topic := client.Topic("topic")

	_, err = Publish(ctx, topic, &pubsub.Message{Data: []byte("hello world!")})
	if err != nil {
		// TODO: Handle error.
	}
}

func ExampleReceive() {
	ctx := context.Background()
	client, err := pubsub.NewClient(ctx, "project-id")
	if err != nil {
		// TODO: Handle error.
	}

	sub := client.Subscription("subscription")

	err = sub.Receive(ctx, ReceiveTracer(sub, func(ctx context.Context, msg *pubsub.Message) {
		// TODO: Handle message.
	}))
	if err != nil {
		// TODO: Handle error.
	}
}
