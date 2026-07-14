// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// v2 admin request→resource mapping. Is implemented as a grpc unary client interceptor
// Uses cloud.google.com/go/pubsub/v2/apiv1/pubsubpb, which is distinct from v1's pubsubpb
// (see admin_v1.go).

package pubsubtrace

import (
	"sync"

	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"google.golang.org/grpc"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// UnaryAdminInterceptorV2 returns a grpc.UnaryClientInterceptor that traces
// TopicAdminClient, SubscriptionAdminClient, and SchemaClient admin operations.
func UnaryAdminInterceptorV2(opts ...Option) grpc.UnaryClientInterceptor {
	return defaultTracerV2().unaryAdminInterceptor(resolveAdminResourceV2, opts...)
}

var (
	v2TracerOnce sync.Once
	v2Tracer     *Tracer
)

func defaultTracerV2() *Tracer {
	v2TracerOnce.Do(func() {
		component := instrumentation.PackageGCPPubsubV2
		v2Tracer = NewTracer(instrumentation.Load(component), component)
	})
	return v2Tracer
}

// resolveAdminResourceV2 maps a v2 admin request to its resource path, or "" if not admin.
func resolveAdminResourceV2(req any) string {
	switch r := req.(type) {
	// TopicAdminClient
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

	// SubscriptionAdminClient
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
