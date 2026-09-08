// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// Package admin provides a gRPC unary client interceptor that traces Google
// Cloud Pub/Sub v2 admin operations.
//
// This package is separate from the parent pubsub contrib so Orchestrion can
// instrument cloud.google.com/go/pubsub/v2/apiv1 without an import cycle through
// the high-level cloud.google.com/go/pubsub/v2 client.
package admin

import (
	"context"
	"strings"
	"sync"

	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"google.golang.org/grpc"

	"github.com/DataDog/dd-trace-go/v2/contrib/cloud.google.com/go/pubsubtrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var (
	tracerOnce sync.Once
	pstrace    *pubsubtrace.Tracer
)

func defaultTracer() *pubsubtrace.Tracer {
	tracerOnce.Do(func() {
		component := instrumentation.PackageGCPPubsubV2
		pstrace = pubsubtrace.NewTracer(instrumentation.Load(component), component)
	})
	return pstrace
}

// UnaryAdminInterceptor returns a grpc.UnaryClientInterceptor that traces
// TopicAdminClient, SubscriptionAdminClient, and SchemaClient admin operations.
//
// When constructing admin clients with option.WithGRPCConn, install this
// interceptor on the dial that creates the connection (WithGRPCDialOption on
// the client constructor is ignored in that case).
func UnaryAdminInterceptor(opts ...pubsubtrace.Option) grpc.UnaryClientInterceptor {
	traceAdmin := defaultTracer().TraceAdmin(opts...)
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, callOpts ...grpc.CallOption) error {
		resourcePath, ok := resolveAdminResource(req)
		if !ok {
			return invoker(ctx, method, req, reply, cc, callOpts...)
		}
		ctx, finish := traceAdmin(ctx, adminMethodName(method), resourcePath)
		err := invoker(ctx, method, req, reply, cc, callOpts...)
		finish(err)
		return err
	}
}

// adminMethodName returns the RPC method name from a gRPC full-method string, e.g.
// "/google.pubsub.v1.Publisher/CreateTopic" -> "CreateTopic".
func adminMethodName(fullMethod string) string {
	if i := strings.LastIndex(fullMethod, "/"); i >= 0 {
		return fullMethod[i+1:]
	}
	return fullMethod
}

// resolveAdminResource maps a v2 admin request to its resource path.
// ok is false for non-admin requests (Publish, Pull, Acknowledge, IAM, ...).
func resolveAdminResource(req any) (resourcePath string, ok bool) {
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
