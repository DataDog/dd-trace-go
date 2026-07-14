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

// resolveAdminResourceV1 maps a v1 admin request to its resource path. Non-admin
// requests (Publish, Pull, Acknowledge, ModifyAckDeadline, IAM, ...) return "".
func resolveAdminResourceV1(req any) string {
	switch r := req.(type) {
	// PublisherClient (topics)
	case *pubsubpb.Topic:
		return r.GetName()
	case *pubsubpb.UpdateTopicRequest:
		return r.GetTopic().GetName()
	case *pubsubpb.GetTopicRequest:
		return r.GetTopic()
	case *pubsubpb.ListTopicsRequest:
		return r.GetProject()
	case *pubsubpb.ListTopicSubscriptionsRequest:
		return r.GetTopic()
	case *pubsubpb.ListTopicSnapshotsRequest:
		return r.GetTopic()
	case *pubsubpb.DeleteTopicRequest:
		return r.GetTopic()
	case *pubsubpb.DetachSubscriptionRequest:
		return r.GetSubscription()

	// SubscriberClient (subscriptions + snapshots)
	case *pubsubpb.Subscription:
		return r.GetName()
	case *pubsubpb.GetSubscriptionRequest:
		return r.GetSubscription()
	case *pubsubpb.UpdateSubscriptionRequest:
		return r.GetSubscription().GetName()
	case *pubsubpb.ListSubscriptionsRequest:
		return r.GetProject()
	case *pubsubpb.DeleteSubscriptionRequest:
		return r.GetSubscription()
	case *pubsubpb.ModifyPushConfigRequest:
		return r.GetSubscription()
	case *pubsubpb.GetSnapshotRequest:
		return r.GetSnapshot()
	case *pubsubpb.ListSnapshotsRequest:
		return r.GetProject()
	case *pubsubpb.CreateSnapshotRequest:
		return r.GetName()
	case *pubsubpb.UpdateSnapshotRequest:
		return r.GetSnapshot().GetName()
	case *pubsubpb.DeleteSnapshotRequest:
		return r.GetSnapshot()
	case *pubsubpb.SeekRequest:
		return r.GetSubscription()

	// SchemaClient
	case *pubsubpb.CreateSchemaRequest:
		return r.GetParent()
	case *pubsubpb.GetSchemaRequest:
		return r.GetName()
	case *pubsubpb.ListSchemasRequest:
		return r.GetParent()
	case *pubsubpb.ListSchemaRevisionsRequest:
		return r.GetName()
	case *pubsubpb.CommitSchemaRequest:
		return r.GetName()
	case *pubsubpb.RollbackSchemaRequest:
		return r.GetName()
	case *pubsubpb.DeleteSchemaRevisionRequest:
		return r.GetName()
	case *pubsubpb.DeleteSchemaRequest:
		return r.GetName()
	case *pubsubpb.ValidateSchemaRequest:
		return r.GetParent()
	case *pubsubpb.ValidateMessageRequest:
		return r.GetParent()

	default:
		return ""
	}
}
