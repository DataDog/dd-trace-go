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

	"github.com/DataDog/dd-trace-go/v2/contrib/cloud.google.com/go/pubsubtrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

const adminProjectID = "project"

func setupAdmin(t *testing.T) (context.Context, mocktracer.Tracer, *vkit.PublisherClient, *vkit.SubscriberClient, *vkit.SchemaClient) {
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)

	srv := pstest.NewServer()
	t.Cleanup(func() { assert.NoError(t, srv.Close()) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	// The admin GAPIC clients issue their RPCs over this connection, so
	// installing the interceptor here traces their admin operations.
	conn, err := grpc.NewClient(srv.Addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(pubsubtrace.UnaryAdminInterceptorV1()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })

	pc, err := vkit.NewPublisherClient(ctx, option.WithGRPCConn(conn))
	require.NoError(t, err)
	sc, err := vkit.NewSubscriberClient(ctx, option.WithGRPCConn(conn))
	require.NoError(t, err)
	schema, err := vkit.NewSchemaClient(ctx, option.WithGRPCConn(conn))
	require.NoError(t, err)

	return ctx, mt, pc, sc, schema
}

func topicName(id string) string {
	return fmt.Sprintf("projects/%s/topics/%s", adminProjectID, id)
}

func subName(id string) string {
	return fmt.Sprintf("projects/%s/subscriptions/%s", adminProjectID, id)
}

func snapshotName(id string) string {
	return fmt.Sprintf("projects/%s/snapshots/%s", adminProjectID, id)
}

func schemaName(id string) string {
	return fmt.Sprintf("projects/%s/schemas/%s", adminProjectID, id)
}

func projectName() string {
	return fmt.Sprintf("projects/%s", adminProjectID)
}

func drain[T any](t *testing.T, next func() (T, error)) {
	t.Helper()
	for {
		if _, err := next(); err == iterator.Done {
			return
		} else {
			require.NoError(t, err)
		}
	}
}

func TestTraceAdminTopicOperations(t *testing.T) {
	ctx, mt, pc, _, _ := setupAdmin(t)

	_, err := pc.CreateTopic(ctx, &pubsubpb.Topic{Name: topicName("topic")})
	require.NoError(t, err)

	_, err = pc.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: topicName("topic")})
	require.NoError(t, err)

	it := pc.ListTopics(ctx, &pubsubpb.ListTopicsRequest{Project: projectName()})
	drain(t, it.Next)

	err = pc.DeleteTopic(ctx, &pubsubpb.DeleteTopicRequest{Topic: topicName("topic")})
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 4)

	assertAdminSpan(t, spans[0], "CreateTopic", "CreateTopic "+topicName("topic"))
	assertAdminSpan(t, spans[1], "GetTopic", "GetTopic "+topicName("topic"))
	assertAdminSpan(t, spans[2], "ListTopics", "ListTopics "+projectName())
	assertAdminSpan(t, spans[3], "DeleteTopic", "DeleteTopic "+topicName("topic"))
}

func TestTraceAdminSubscriptionOperations(t *testing.T) {
	ctx, mt, pc, sc, _ := setupAdmin(t)

	_, err := pc.CreateTopic(ctx, &pubsubpb.Topic{Name: topicName("topic")})
	require.NoError(t, err)

	_, err = sc.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name:  subName("sub"),
		Topic: topicName("topic"),
	})
	require.NoError(t, err)

	_, err = sc.GetSubscription(ctx, &pubsubpb.GetSubscriptionRequest{Subscription: subName("sub")})
	require.NoError(t, err)

	it := sc.ListSubscriptions(ctx, &pubsubpb.ListSubscriptionsRequest{Project: projectName()})
	drain(t, it.Next)

	err = sc.DeleteSubscription(ctx, &pubsubpb.DeleteSubscriptionRequest{Subscription: subName("sub")})
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 5)

	assertAdminSpan(t, spans[0], "CreateTopic", "CreateTopic "+topicName("topic"))
	assertAdminSpan(t, spans[1], "CreateSubscription", "CreateSubscription "+subName("sub"))
	assertAdminSpan(t, spans[2], "GetSubscription", "GetSubscription "+subName("sub"))
	assertAdminSpan(t, spans[3], "ListSubscriptions", "ListSubscriptions "+projectName())
	assertAdminSpan(t, spans[4], "DeleteSubscription", "DeleteSubscription "+subName("sub"))
}

func TestTraceAdminSnapshotOperations(t *testing.T) {
	ctx, mt, pc, sc, _ := setupAdmin(t)

	_, err := pc.CreateTopic(ctx, &pubsubpb.Topic{Name: topicName("topic")})
	require.NoError(t, err)
	_, err = sc.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name:  subName("sub"),
		Topic: topicName("topic"),
	})
	require.NoError(t, err)

	// pstest does not implement snapshots, so these RPCs error — but the
	// interceptor must still emit spans with the resolved resource path.
	_, err = sc.CreateSnapshot(ctx, &pubsubpb.CreateSnapshotRequest{
		Name:         snapshotName("snap"),
		Subscription: subName("sub"),
	})
	require.Error(t, err)

	_, err = sc.GetSnapshot(ctx, &pubsubpb.GetSnapshotRequest{Snapshot: snapshotName("snap")})
	require.Error(t, err)

	it := sc.ListSnapshots(ctx, &pubsubpb.ListSnapshotsRequest{Project: projectName()})
	_, err = it.Next()
	require.Error(t, err)
	require.NotEqual(t, iterator.Done, err)

	err = sc.DeleteSnapshot(ctx, &pubsubpb.DeleteSnapshotRequest{Snapshot: snapshotName("snap")})
	require.Error(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 6)

	assertAdminSpan(t, spans[0], "CreateTopic", "CreateTopic "+topicName("topic"))
	assertAdminSpan(t, spans[1], "CreateSubscription", "CreateSubscription "+subName("sub"))
	assertAdminSpan(t, spans[2], "CreateSnapshot", "CreateSnapshot "+snapshotName("snap"))
	assert.NotNil(t, spans[2].Tag(ext.ErrorMsg))
	assertAdminSpan(t, spans[3], "GetSnapshot", "GetSnapshot "+snapshotName("snap"))
	assert.NotNil(t, spans[3].Tag(ext.ErrorMsg))
	assertAdminSpan(t, spans[4], "ListSnapshots", "ListSnapshots "+projectName())
	assert.NotNil(t, spans[4].Tag(ext.ErrorMsg))
	assertAdminSpan(t, spans[5], "DeleteSnapshot", "DeleteSnapshot "+snapshotName("snap"))
	assert.NotNil(t, spans[5].Tag(ext.ErrorMsg))
}

func TestTraceAdminSchemaOperations(t *testing.T) {
	ctx, mt, _, _, sc := setupAdmin(t)

	const avroDef = `{"type":"record","name":"Test","fields":[{"name":"f","type":"string"}]}`
	_, err := sc.CreateSchema(ctx, &pubsubpb.CreateSchemaRequest{
		Parent: projectName(),
		Schema: &pubsubpb.Schema{
			Type:       pubsubpb.Schema_AVRO,
			Definition: avroDef,
		},
		SchemaId: "schema",
	})
	require.NoError(t, err)

	_, err = sc.GetSchema(ctx, &pubsubpb.GetSchemaRequest{Name: schemaName("schema")})
	require.NoError(t, err)

	it := sc.ListSchemas(ctx, &pubsubpb.ListSchemasRequest{Parent: projectName()})
	drain(t, it.Next)

	err = sc.DeleteSchema(ctx, &pubsubpb.DeleteSchemaRequest{Name: schemaName("schema")})
	require.NoError(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 4)

	assertAdminSpan(t, spans[0], "CreateSchema", "CreateSchema "+projectName())
	assertAdminSpan(t, spans[1], "GetSchema", "GetSchema "+schemaName("schema"))
	assertAdminSpan(t, spans[2], "ListSchemas", "ListSchemas "+projectName())
	assertAdminSpan(t, spans[3], "DeleteSchema", "DeleteSchema "+schemaName("schema"))
}

func TestTraceAdminError(t *testing.T) {
	ctx, mt, pc, _, _ := setupAdmin(t)

	_, err := pc.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: topicName("missing")})
	require.Error(t, err)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, err.Error(), spans[0].Tag(ext.ErrorMsg))
	assertAdminSpan(t, spans[0], "GetTopic", "GetTopic "+topicName("missing"))
}

func TestTraceAdminMissingResource(t *testing.T) {
	ctx, mt, pc, _, _ := setupAdmin(t)

	// Recognized admin RPCs with an empty resource field must still emit a
	// span; TraceAdmin falls back to a method-only resource name.
	_, createErr := pc.CreateTopic(ctx, &pubsubpb.Topic{})
	_, getErr := pc.GetTopic(ctx, &pubsubpb.GetTopicRequest{})

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)

	assert.Equal(t, "gcp.pubsub.request", spans[0].OperationName())
	assert.Equal(t, "CreateTopic", spans[0].Tag(ext.ResourceName))
	assert.Equal(t, "CreateTopic", spans[0].Tag("pubsub.method"))
	assert.Nil(t, spans[0].Tag(ext.GCPProjectID))
	if createErr != nil {
		assert.Equal(t, createErr.Error(), spans[0].Tag(ext.ErrorMsg))
	}

	assert.Equal(t, "gcp.pubsub.request", spans[1].OperationName())
	assert.Equal(t, "GetTopic", spans[1].Tag(ext.ResourceName))
	assert.Equal(t, "GetTopic", spans[1].Tag("pubsub.method"))
	assert.Nil(t, spans[1].Tag(ext.GCPProjectID))
	if getErr != nil {
		assert.Equal(t, getErr.Error(), spans[1].Tag(ext.ErrorMsg))
	}
}

func TestTraceAdminWithService(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	srv := pstest.NewServer()
	defer func() { assert.NoError(t, srv.Close()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(srv.Addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(pubsubtrace.UnaryAdminInterceptorV1(WithService("my-admin-service"))),
	)
	require.NoError(t, err)
	defer func() { assert.NoError(t, conn.Close()) }()

	pc, err := vkit.NewPublisherClient(ctx, option.WithGRPCConn(conn))
	require.NoError(t, err)

	_, err = pc.CreateTopic(ctx, &pubsubpb.Topic{Name: topicName("topic")})
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
	assert.Equal(t, adminProjectID, span.Tag(ext.GCPProjectID))
	assert.Equal(t, "cloud.google.com/go/pubsub.v1", span.Integration())
}
