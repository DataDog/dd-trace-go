// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package gcppubsub

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/containers"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/modules/gcloud"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	testTopic        = "pstest-orchestrion-topic"
	testSubscription = "pstest-orchestrion-subscription"
)

type TestCase struct {
	container   *gcloud.GCloudContainer
	client      *pubsub.Client
	publishTime time.Time
	messageID   string
}

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	var err error

	tc.container, err = gcloud.RunPubsub(ctx,
		"gcr.io/google.com/cloudsdktool/google-cloud-cli:emulators",
		gcloud.WithProjectID("pstest-orchestrion"),
		testcontainers.WithLogger(tclog.TestLogger(t)),
		containers.WithTestLogConsumer(t),
	)
	containers.AssertTestContainersError(t, err)
	containers.RegisterContainerCleanup(t, tc.container)

	projectID := tc.container.Settings.ProjectID

	//orchestrion:ignore
	conn, err := grpc.NewClient(tc.container.URI, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	tc.client, err = pubsub.NewClient(ctx, projectID, option.WithGRPCConn(conn))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, tc.client.Close()) })

	topic, err := tc.client.CreateTopic(ctx, testTopic)
	require.NoError(t, err)

	_, err = tc.client.CreateSubscription(ctx, testSubscription, pubsub.SubscriptionConfig{
		Topic:                 topic,
		EnableMessageOrdering: true,
	})
	require.NoError(t, err)
}

func (tc *TestCase) publishMessage(ctx context.Context, t *testing.T) {
	topic := tc.client.Topic(testTopic)
	topic.EnableMessageOrdering = true
	res := topic.Publish(ctx, &pubsub.Message{
		Data:        []byte("Hello, World!"),
		OrderingKey: "ordering-key",
	})
	id, err := res.Get(ctx)
	require.NoError(t, err)
	t.Log("finished publishing result", id)
}

func (tc *TestCase) receiveMessage(ctx context.Context, t *testing.T) {
	// We use a cancellable context so we can stop listening for more messages as
	// soon as we have processed one. The [pubsub.Subscription.Receive] method
	// keeps listening until a non-retryable error occurs, or the context gets
	// cancelled... This would effectively block the test forever!
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // In case the message never arrives...

	sub := tc.client.Subscription(testSubscription)
	err := sub.Receive(ctx, func(_ context.Context, message *pubsub.Message) {
		assert.Equal(t, message.Data, []byte("Hello, World!"))
		message.Ack()
		tc.publishTime = message.PublishTime
		tc.messageID = message.ID
		cancel() // Stop waiting for more messages immediately...
	})
	require.NoError(t, err)

	// Ensure the context is not done yet...
	require.NotErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	tc.publishMessage(ctx, t)
	tc.receiveMessage(ctx, t)
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "pubsub.publish",
				"type":     "queue",
				"resource": "projects/pstest-orchestrion/topics/pstest-orchestrion-topic",
				"service":  "gcp_pubsub.test",
			},
			Meta: map[string]string{
				"span.kind":    "producer",
				"component":    "cloud.google.com/go/pubsub.v1",
				"ordering_key": "ordering-key",
			},
			Children: trace.Traces{
				{
					Tags: map[string]any{
						"name":     "pubsub.receive",
						"type":     "queue",
						"resource": "projects/pstest-orchestrion/subscriptions/pstest-orchestrion-subscription",
						"service":  "gcp_pubsub.test",
					},
					Meta: map[string]string{
						"span.kind":        "consumer",
						"component":        "cloud.google.com/go/pubsub.v1",
						"messaging.system": "googlepubsub",
						"ordering_key":     "ordering-key",
						"publish_time":     tc.publishTime.String(),
						"message_id":       tc.messageID,
					},
				},
			},
		},
	}
}
