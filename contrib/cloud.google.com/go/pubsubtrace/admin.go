// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// v2 admin request→resource mapping, implemented as a grpc unary client interceptor.
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
//
// When constructing admin clients with option.WithGRPCConn, install this
// interceptor on the dial that creates the connection (WithGRPCDialOption on
// the client constructor is ignored in that case).
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

// resolveAdminResourceV2 maps a v2 admin request to its resource path.
// ok is false for non-admin requests (Publish, Pull, Acknowledge, IAM, ...).
func resolveAdminResourceV2(req any) (resourcePath string, ok bool) {
	switch r := req.(type) {
	// TopicAdminClient
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

	// SubscriptionAdminClient
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
