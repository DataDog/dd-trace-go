// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/v2/pstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pubsubtrace "github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var gcpPubsubV2 = harness.TestCase{
	Name: instrumentation.PackageGCPPubsub,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		mt := mocktracer.Start()
		defer mt.Stop()

		var opts []pubsubtrace.Option
		if serviceOverride != "" {
			opts = append(opts, pubsubtrace.WithService(serviceOverride))
		}
		pub, sub, srv, cleanup := newTestGCPPubsubV2(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		_, err := pubsubtrace.Publish(ctx, pub, &pubsub.Message{Data: []byte("hello"), OrderingKey: "xxx"}, opts...).Get(ctx)
		require.NoError(t, err)

		done := make(chan struct{})
		go func() {
			err := sub.Receive(ctx, pubsubtrace.WrapReceiveHandler(sub, func(ctx context.Context, msg *pubsub.Message) {
				msg.Ack()
				close(done)
			}, opts...))
			if err != nil {
				if st, ok := status.FromError(err); !ok || st.Code() != codes.Canceled {
					t.Logf("sub.Receive failed: %v", err)
				}
			}
		}()

		<-done
		cancel()
		cleanup()
		srv.Wait()

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"", ""},
		DDService:       []string{"", ""},
		ServiceOverride: []string{harness.TestServiceOverride, harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "pubsub.publish", spans[0].OperationName())
		assert.Equal(t, "pubsub.receive", spans[1].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "gcp.pubsub.send", spans[0].OperationName())
		assert.Equal(t, "gcp.pubsub.process", spans[1].OperationName())
	},
}

func newTestGCPPubsubV2(t *testing.T) (*pubsub.Publisher, *pubsub.Subscriber, *pstest.Server, func()) {
	srv := pstest.NewServer()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)

	const projectID = "project"
	client, err := pubsub.NewClient(ctx, projectID, option.WithGRPCConn(conn))
	require.NoError(t, err)

	topic, err := client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{
		Name: fmt.Sprintf("projects/%s/topics/topic", projectID),
	})
	require.NoError(t, err)

	publisher := client.Publisher(topic.Name)
	publisher.EnableMessageOrdering = true

	subscription, err := client.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name:  fmt.Sprintf("projects/%s/subscriptions/subscription", projectID),
		Topic: topic.Name,
	})
	require.NoError(t, err)

	sub := client.Subscriber(subscription.Name)
	return publisher, sub, srv, func() {
		assert.NoError(t, conn.Close())
		assert.NoError(t, srv.Close())
	}
}
