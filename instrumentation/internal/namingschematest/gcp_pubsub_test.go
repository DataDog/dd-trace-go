package namingschematest

import (
	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/pubsub/pstest"
	"context"
	pubsubtrace "github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"testing"
	"time"
)

var gcpPubsub = testCase{
	name: instrumentation.PackageCloudGoogleComPubsub,
	genSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		mt := mocktracer.Start()
		defer mt.Stop()

		var opts []pubsubtrace.Option
		if serviceOverride != "" {
			opts = append(opts, pubsubtrace.WithService(serviceOverride))
		}
		topic, sub := newTestGCPPubsub(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		_, err := pubsubtrace.Publish(ctx, topic, &pubsub.Message{Data: []byte("hello"), OrderingKey: "xxx"}, opts...).Get(ctx)
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
		return mt.FinishedSpans()
	},
	wantServiceNameV0: serviceNameAssertions{
		defaults:        []string{"", ""},
		ddService:       []string{"", ""},
		serviceOverride: []string{testServiceOverride, testServiceOverride},
	},
	assertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "pubsub.publish", spans[0].OperationName())
		assert.Equal(t, "pubsub.receive", spans[1].OperationName())
	},
	assertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 2)
		assert.Equal(t, "gcp.pubsub.send", spans[0].OperationName())
		assert.Equal(t, "gcp.pubsub.process", spans[1].OperationName())
	},
}

func newTestGCPPubsub(t *testing.T) (*pubsub.Topic, *pubsub.Subscription) {
	srv := pstest.NewServer()
	t.Cleanup(func() { assert.NoError(t, srv.Close()) })

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })

	client, err := pubsub.NewClient(ctx, "project", option.WithGRPCConn(conn))
	require.NoError(t, err)

	_, err = client.CreateTopic(ctx, "topic")
	require.NoError(t, err)

	topic := client.Topic("topic")
	topic.EnableMessageOrdering = true
	_, err = client.CreateSubscription(ctx, "subscription", pubsub.SubscriptionConfig{
		Topic: topic,
	})
	require.NoError(t, err)

	sub := client.Subscription("subscription")
	return topic, sub
}
