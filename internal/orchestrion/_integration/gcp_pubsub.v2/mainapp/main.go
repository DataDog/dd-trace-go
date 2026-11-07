// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Command mainapp publishes, receives and calls the admin clients of
// cloud.google.com/go/pubsub/v2 from package main, which the harness suites never
// build. TestMainApp builds and runs it.
package main

import (
	"context"
	"fmt"

	"cloud.google.com/go/pubsub/v2"
	vkit "cloud.google.com/go/pubsub/v2/apiv1"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/v2/pstest"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/api/option/internaloption"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const project = "mainapp"

func main() {
	ctx := context.Background()
	srv := pstest.NewServer()
	defer srv.Close()

	opts := []option.ClientOption{
		option.WithEndpoint(srv.Addr),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		option.WithoutAuthentication(),
		internaloption.SkipDialSettingsValidation(),
	}

	client := must(pubsub.NewClient(ctx, project, opts...))
	defer client.Close()

	topic := must(client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{
		Name: "projects/" + project + "/topics/main-topic",
	}))
	sub := must(client.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name:  "projects/" + project + "/subscriptions/main-subscription",
		Topic: topic.Name,
	}))

	publisher := client.Publisher(topic.Name)
	must(publisher.Publish(ctx, &pubsub.Message{Data: []byte("main")}).Get(ctx))
	publisher.Stop()

	rctx, cancel := context.WithCancel(ctx)
	check(client.Subscriber(sub.Name).Receive(rctx, func(_ context.Context, msg *pubsub.Message) {
		msg.Ack()
		cancel()
	}))

	pc := must(vkit.NewTopicAdminClient(ctx, opts...))
	it := pc.ListTopics(ctx, &pubsubpb.ListTopicsRequest{Project: "projects/" + project})
	for {
		if _, err := it.Next(); err == iterator.Done {
			break
		} else {
			check(err)
		}
	}
	check(pc.Close())

	listSchemas(ctx, srv.Addr)

	// The injected `defer tracer.Stop()` flushes the spans, so main has to
	// return normally.
	fmt.Println("mainapp: done")
}

func must[T any](v T, err error) T {
	check(err)
	return v
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
