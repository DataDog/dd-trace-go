// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// v1 admin request→resource mapping. Uses cloud.google.com/go/pubsub/apiv1/pubsubpb,
// which distinct from v2's pubsubpb (see admin.go).

package pubsubtrace

import (
	"sync"

	pubsubpb "cloud.google.com/go/pubsub/apiv1/pubsubpb"
	"google.golang.org/grpc"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// UnaryAdminInterceptorV1 returns a grpc.UnaryClientInterceptor that traces the admin
// operations of the cloud.google.com/go/pubsub (v1) GAPIC clients (PublisherClient,
// SubscriberClient, SchemaClient).
//
// When constructing admin clients with option.WithGRPCConn, install this
// interceptor on the dial that creates the connection (WithGRPCDialOption on
// the client constructor is ignored in that case).
func UnaryAdminInterceptorV1(opts ...Option) grpc.UnaryClientInterceptor {
	return defaultTracerV1().unaryAdminInterceptor(resolveAdminResourceV1, opts...)
}

var (
	v1TracerOnce sync.Once
	v1Tracer     *Tracer
)

func defaultTracerV1() *Tracer {
	v1TracerOnce.Do(func() {
		component := instrumentation.PackageGCPPubsub
		v1Tracer = NewTracer(instrumentation.Load(component), component)
	})
	return v1Tracer
}

// resolveAdminResourceV1 maps a v1 admin request to its resource path.
// ok is false for non-admin requests (Publish, Pull, Acknowledge, ModifyAckDeadline, IAM, ...).
func resolveAdminResourceV1(req any) (resourcePath string, ok bool) {
	switch r := req.(type) {
	// PublisherClient (topics)
	case *pubsubpb.Topic:
		return r.GetName(), true
	case *pubsubpb.UpdateTopicRequest:
		return r.GetTopic().GetName(), true
	case *pubsubpb.GetTopicRequest:
		return r.GetTopic(), true
	case *pubsubpb.ListTopicsRequest:
		return r.GetProject(), true
	case *pubsubpb.ListTopicSubscriptionsRequest:
		return r.GetTopic(), true
	case *pubsubpb.ListTopicSnapshotsRequest:
		return r.GetTopic(), true
	case *pubsubpb.DeleteTopicRequest:
		return r.GetTopic(), true
	case *pubsubpb.DetachSubscriptionRequest:
		return r.GetSubscription(), true

	// SubscriberClient (subscriptions + snapshots)
	case *pubsubpb.Subscription:
		return r.GetName(), true
	case *pubsubpb.GetSubscriptionRequest:
		return r.GetSubscription(), true
	case *pubsubpb.UpdateSubscriptionRequest:
		return r.GetSubscription().GetName(), true
	case *pubsubpb.ListSubscriptionsRequest:
		return r.GetProject(), true
	case *pubsubpb.DeleteSubscriptionRequest:
		return r.GetSubscription(), true
	case *pubsubpb.ModifyPushConfigRequest:
		return r.GetSubscription(), true
	case *pubsubpb.GetSnapshotRequest:
		return r.GetSnapshot(), true
	case *pubsubpb.ListSnapshotsRequest:
		return r.GetProject(), true
	case *pubsubpb.CreateSnapshotRequest:
		return r.GetName(), true
	case *pubsubpb.UpdateSnapshotRequest:
		return r.GetSnapshot().GetName(), true
	case *pubsubpb.DeleteSnapshotRequest:
		return r.GetSnapshot(), true
	case *pubsubpb.SeekRequest:
		return r.GetSubscription(), true

	// SchemaClient
	case *pubsubpb.CreateSchemaRequest:
		return r.GetParent(), true
	case *pubsubpb.GetSchemaRequest:
		return r.GetName(), true
	case *pubsubpb.ListSchemasRequest:
		return r.GetParent(), true
	case *pubsubpb.ListSchemaRevisionsRequest:
		return r.GetName(), true
	case *pubsubpb.CommitSchemaRequest:
		return r.GetName(), true
	case *pubsubpb.RollbackSchemaRequest:
		return r.GetName(), true
	case *pubsubpb.DeleteSchemaRevisionRequest:
		return r.GetName(), true
	case *pubsubpb.DeleteSchemaRequest:
		return r.GetName(), true
	case *pubsubpb.ValidateSchemaRequest:
		return r.GetParent(), true
	case *pubsubpb.ValidateMessageRequest:
		return r.GetParent(), true

	default:
		return "", false
	}
}
