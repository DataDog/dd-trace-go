// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// helpers for Pub/Sub v1 admin tracing. These mirror admin.go but take v1
// pubsubpb types (cloud.google.com/go/pubsub/apiv1/pubsubpb), which are distinct
// Go types from v2's pubsubpb
//
// v1 distributes admin RPCs across the PublisherClient (topics) and SubscriberClient
// (subscriptions + snapshots) GAPIC clients, plus the SchemaClient. v2 separates
// these into TopicAdminClient/SubscriptionAdminClient/SchemaClient. The set of RPCs
// is the same; only the home struct differs.

package pubsubtrace

import (
	"context"

	pubsubpb "cloud.google.com/go/pubsub/apiv1/pubsubpb"
)

// -- PublisherClient (topics) --

func (tr *Tracer) TraceCreateTopicV1(ctx context.Context, req *pubsubpb.Topic, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "CreateTopic", req.GetName(), opts...)
}

func (tr *Tracer) TraceUpdateTopicV1(ctx context.Context, req *pubsubpb.UpdateTopicRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "UpdateTopic", req.GetTopic().GetName(), opts...)
}

func (tr *Tracer) TraceGetTopicV1(ctx context.Context, req *pubsubpb.GetTopicRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "GetTopic", req.GetTopic(), opts...)
}

func (tr *Tracer) TraceListTopicsV1(ctx context.Context, req *pubsubpb.ListTopicsRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListTopics", req.GetProject(), opts...)
}

func (tr *Tracer) TraceListTopicSubscriptionsV1(ctx context.Context, req *pubsubpb.ListTopicSubscriptionsRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListTopicSubscriptions", req.GetTopic(), opts...)
}

func (tr *Tracer) TraceListTopicSnapshotsV1(ctx context.Context, req *pubsubpb.ListTopicSnapshotsRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListTopicSnapshots", req.GetTopic(), opts...)
}

func (tr *Tracer) TraceDeleteTopicV1(ctx context.Context, req *pubsubpb.DeleteTopicRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "DeleteTopic", req.GetTopic(), opts...)
}

func (tr *Tracer) TraceDetachSubscriptionV1(ctx context.Context, req *pubsubpb.DetachSubscriptionRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "DetachSubscription", req.GetSubscription(), opts...)
}

// -- SubscriberClient (subscriptions + snapshots) --

func (tr *Tracer) TraceCreateSubscriptionV1(ctx context.Context, req *pubsubpb.Subscription, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "CreateSubscription", req.GetName(), opts...)
}

func (tr *Tracer) TraceGetSubscriptionV1(ctx context.Context, req *pubsubpb.GetSubscriptionRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "GetSubscription", req.GetSubscription(), opts...)
}

func (tr *Tracer) TraceUpdateSubscriptionV1(ctx context.Context, req *pubsubpb.UpdateSubscriptionRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "UpdateSubscription", req.GetSubscription().GetName(), opts...)
}

func (tr *Tracer) TraceListSubscriptionsV1(ctx context.Context, req *pubsubpb.ListSubscriptionsRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListSubscriptions", req.GetProject(), opts...)
}

func (tr *Tracer) TraceDeleteSubscriptionV1(ctx context.Context, req *pubsubpb.DeleteSubscriptionRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "DeleteSubscription", req.GetSubscription(), opts...)
}

func (tr *Tracer) TraceModifyPushConfigV1(ctx context.Context, req *pubsubpb.ModifyPushConfigRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ModifyPushConfig", req.GetSubscription(), opts...)
}

func (tr *Tracer) TraceGetSnapshotV1(ctx context.Context, req *pubsubpb.GetSnapshotRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "GetSnapshot", req.GetSnapshot(), opts...)
}

func (tr *Tracer) TraceListSnapshotsV1(ctx context.Context, req *pubsubpb.ListSnapshotsRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListSnapshots", req.GetProject(), opts...)
}

func (tr *Tracer) TraceCreateSnapshotV1(ctx context.Context, req *pubsubpb.CreateSnapshotRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "CreateSnapshot", req.GetName(), opts...)
}

func (tr *Tracer) TraceUpdateSnapshotV1(ctx context.Context, req *pubsubpb.UpdateSnapshotRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "UpdateSnapshot", req.GetSnapshot().GetName(), opts...)
}

func (tr *Tracer) TraceDeleteSnapshotV1(ctx context.Context, req *pubsubpb.DeleteSnapshotRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "DeleteSnapshot", req.GetSnapshot(), opts...)
}

func (tr *Tracer) TraceSeekV1(ctx context.Context, req *pubsubpb.SeekRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "Seek", req.GetSubscription(), opts...)
}

// -- SchemaClient --

func (tr *Tracer) TraceCreateSchemaV1(ctx context.Context, req *pubsubpb.CreateSchemaRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "CreateSchema", req.GetParent(), opts...)
}

func (tr *Tracer) TraceGetSchemaV1(ctx context.Context, req *pubsubpb.GetSchemaRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "GetSchema", req.GetName(), opts...)
}

func (tr *Tracer) TraceListSchemasV1(ctx context.Context, req *pubsubpb.ListSchemasRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListSchemas", req.GetParent(), opts...)
}

func (tr *Tracer) TraceListSchemaRevisionsV1(ctx context.Context, req *pubsubpb.ListSchemaRevisionsRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListSchemaRevisions", req.GetName(), opts...)
}

func (tr *Tracer) TraceCommitSchemaV1(ctx context.Context, req *pubsubpb.CommitSchemaRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "CommitSchema", req.GetName(), opts...)
}

func (tr *Tracer) TraceRollbackSchemaV1(ctx context.Context, req *pubsubpb.RollbackSchemaRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "RollbackSchema", req.GetName(), opts...)
}

func (tr *Tracer) TraceDeleteSchemaRevisionV1(ctx context.Context, req *pubsubpb.DeleteSchemaRevisionRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "DeleteSchemaRevision", req.GetName(), opts...)
}

func (tr *Tracer) TraceDeleteSchemaV1(ctx context.Context, req *pubsubpb.DeleteSchemaRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "DeleteSchema", req.GetName(), opts...)
}

func (tr *Tracer) TraceValidateSchemaV1(ctx context.Context, req *pubsubpb.ValidateSchemaRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ValidateSchema", req.GetParent(), opts...)
}

func (tr *Tracer) TraceValidateMessageV1(ctx context.Context, req *pubsubpb.ValidateMessageRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ValidateMessage", req.GetParent(), opts...)
}
