// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package pubsub

import (
	"context"
	"fmt"
	"testing"
	"time"

	vkit "cloud.google.com/go/pubsub/apiv1"
	pubsubpb "cloud.google.com/go/pubsub/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/pstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

const adminProjectID = "project"

func setupAdmin(t *testing.T) (context.Context, mocktracer.Tracer, *PublisherClient, *SubscriberClient) {
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)

	srv := pstest.NewServer()
	t.Cleanup(func() { assert.NoError(t, srv.Close()) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })

	pc, err := vkit.NewPublisherClient(ctx, option.WithGRPCConn(conn))
	require.NoError(t, err)
	sc, err := vkit.NewSubscriberClient(ctx, option.WithGRPCConn(conn))
	require.NoError(t, err)

	return ctx, mt, WrapPublisherClient(pc), WrapSubscriberClient(sc)
}

func topicName(id string) string {
	return fmt.Sprintf("projects/%s/topics/%s", adminProjectID, id)
}

func subName(id string) string {
	return fmt.Sprintf("projects/%s/subscriptions/%s", adminProjectID, id)
}

func TestTraceAdminTopicOperations(t *testing.T) {
	ctx, mt, pc, _ := setupAdmin(t)

	_, err := pc.CreateTopic(ctx, &pubsubpb.Topic{Name: topicName("topic")})
	require.NoError(t, err)

	_, err = pc.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: topicName("topic")})
	require.NoError(t, err)

	it := pc.ListTopics(ctx, &pubsubpb.ListTopicsRequest{Project: fmt.Sprintf("projects/%s", adminProjectID)})
	for {
		if _, err := it.Next(); err == iterator.Done {
			break
		} else {
			require.NoError(t, err)
		}
	}

	err = pc.DeleteTopic(ctx, &pubsubpb.DeleteTopicRequest{Topic: topicName("topic")})
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 4)

	assertAdminSpan(t, spans[0], "CreateTopic", "CreateTopic "+topicName("topic"))
	assertAdminSpan(t, spans[1], "GetTopic", "GetTopic "+topicName("topic"))
	assertAdminSpan(t, spans[2], "ListTopics", fmt.Sprintf("ListTopics projects/%s", adminProjectID))
	assertAdminSpan(t, spans[3], "DeleteTopic", "DeleteTopic "+topicName("topic"))
}

func TestTraceAdminSubscriptionOperations(t *testing.T) {
	ctx, mt, pc, sc := setupAdmin(t)

	_, err := pc.CreateTopic(ctx, &pubsubpb.Topic{Name: topicName("topic")})
	require.NoError(t, err)

	_, err = sc.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name:  subName("sub"),
		Topic: topicName("topic"),
	})
	require.NoError(t, err)

	_, err = sc.GetSubscription(ctx, &pubsubpb.GetSubscriptionRequest{Subscription: subName("sub")})
	require.NoError(t, err)

	err = sc.DeleteSubscription(ctx, &pubsubpb.DeleteSubscriptionRequest{Subscription: subName("sub")})
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 4)

	assertAdminSpan(t, spans[0], "CreateTopic", "CreateTopic "+topicName("topic"))
	assertAdminSpan(t, spans[1], "CreateSubscription", "CreateSubscription "+subName("sub"))
	assertAdminSpan(t, spans[2], "GetSubscription", "GetSubscription "+subName("sub"))
	assertAdminSpan(t, spans[3], "DeleteSubscription", "DeleteSubscription "+subName("sub"))
}

func TestTraceAdminError(t *testing.T) {
	ctx, mt, pc, _ := setupAdmin(t)

	_, err := pc.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: topicName("missing")})
	require.Error(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, err.Error(), spans[0].Tag(ext.ErrorMsg))
	assertAdminSpan(t, spans[0], "GetTopic", "GetTopic "+topicName("missing"))
}

func TestTraceAdminWithService(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := pstest.NewServer()
	defer func() { assert.NoError(t, srv.Close()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { assert.NoError(t, conn.Close()) }()

	pc, err := vkit.NewPublisherClient(ctx, option.WithGRPCConn(conn))
	require.NoError(t, err)

	wrapped := WrapPublisherClient(pc, WithService("my-admin-service"))
	_, err = wrapped.CreateTopic(ctx, &pubsubpb.Topic{Name: topicName("topic")})
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "my-admin-service", spans[0].Tag(ext.ServiceName))
}

func assertAdminSpan(t *testing.T, span *mocktracer.Span, method, resource string) {
	t.Helper()
	assert.Equal(t, "gcp.pubsub.request", span.OperationName())
	assert.Equal(t, resource, span.Tag(ext.ResourceName))
	assert.Equal(t, ext.SpanTypeWorker, span.Tag(ext.SpanType))
	assert.Equal(t, ext.SpanKindClient, span.Tag(ext.SpanKind))
	assert.Equal(t, "cloud.google.com/go/pubsub.v1", span.Tag(ext.Component))
	assert.Equal(t, ext.MessagingSystemGCPPubsub, span.Tag(ext.MessagingSystem))
	assert.Equal(t, method, span.Tag("pubsub.method"))
	assert.Equal(t, adminProjectID, span.Tag("gcloud.project_id"))
	assert.Equal(t, "cloud.google.com/go/pubsub.v1", span.Integration())
}
