// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

// helpers for Pub/Sub v2 admin tracing.
//
// Each helper exists so the manual instrumentation in contrib/.../pubsub.v2/admin.go
// and the orchestrion aspects in contrib/.../pubsub.v2/orchestrion.yml can share a
// single (method, resourcePath) mapping. Without these helpers the same span tag may
// drift between the two call paths.

package pubsubtrace

import (
	"context"

	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
)

// -- TopicAdminClient --

func (tr *Tracer) TraceCreateTopic(ctx context.Context, req *pubsubpb.Topic, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "CreateTopic", req.GetName(), opts...)
}

func (tr *Tracer) TraceUpdateTopic(ctx context.Context, req *pubsubpb.UpdateTopicRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "UpdateTopic", req.GetTopic().GetName(), opts...)
}

func (tr *Tracer) TraceGetTopic(ctx context.Context, req *pubsubpb.GetTopicRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "GetTopic", req.GetTopic(), opts...)
}

func (tr *Tracer) TraceListTopics(ctx context.Context, req *pubsubpb.ListTopicsRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListTopics", req.GetProject(), opts...)
}

func (tr *Tracer) TraceListTopicSubscriptions(ctx context.Context, req *pubsubpb.ListTopicSubscriptionsRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListTopicSubscriptions", req.GetTopic(), opts...)
}

func (tr *Tracer) TraceListTopicSnapshots(ctx context.Context, req *pubsubpb.ListTopicSnapshotsRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListTopicSnapshots", req.GetTopic(), opts...)
}

func (tr *Tracer) TraceDeleteTopic(ctx context.Context, req *pubsubpb.DeleteTopicRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "DeleteTopic", req.GetTopic(), opts...)
}

func (tr *Tracer) TraceDetachSubscription(ctx context.Context, req *pubsubpb.DetachSubscriptionRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "DetachSubscription", req.GetSubscription(), opts...)
}

// -- SubscriptionAdminClient --

func (tr *Tracer) TraceCreateSubscription(ctx context.Context, req *pubsubpb.Subscription, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "CreateSubscription", req.GetName(), opts...)
}

func (tr *Tracer) TraceGetSubscription(ctx context.Context, req *pubsubpb.GetSubscriptionRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "GetSubscription", req.GetSubscription(), opts...)
}

func (tr *Tracer) TraceUpdateSubscription(ctx context.Context, req *pubsubpb.UpdateSubscriptionRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "UpdateSubscription", req.GetSubscription().GetName(), opts...)
}

func (tr *Tracer) TraceListSubscriptions(ctx context.Context, req *pubsubpb.ListSubscriptionsRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListSubscriptions", req.GetProject(), opts...)
}

func (tr *Tracer) TraceDeleteSubscription(ctx context.Context, req *pubsubpb.DeleteSubscriptionRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "DeleteSubscription", req.GetSubscription(), opts...)
}

func (tr *Tracer) TraceModifyPushConfig(ctx context.Context, req *pubsubpb.ModifyPushConfigRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ModifyPushConfig", req.GetSubscription(), opts...)
}

func (tr *Tracer) TraceGetSnapshot(ctx context.Context, req *pubsubpb.GetSnapshotRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "GetSnapshot", req.GetSnapshot(), opts...)
}

func (tr *Tracer) TraceListSnapshots(ctx context.Context, req *pubsubpb.ListSnapshotsRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListSnapshots", req.GetProject(), opts...)
}

func (tr *Tracer) TraceCreateSnapshot(ctx context.Context, req *pubsubpb.CreateSnapshotRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "CreateSnapshot", req.GetName(), opts...)
}

func (tr *Tracer) TraceUpdateSnapshot(ctx context.Context, req *pubsubpb.UpdateSnapshotRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "UpdateSnapshot", req.GetSnapshot().GetName(), opts...)
}

func (tr *Tracer) TraceDeleteSnapshot(ctx context.Context, req *pubsubpb.DeleteSnapshotRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "DeleteSnapshot", req.GetSnapshot(), opts...)
}

func (tr *Tracer) TraceSeek(ctx context.Context, req *pubsubpb.SeekRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "Seek", req.GetSubscription(), opts...)
}

// -- SchemaClient --

func (tr *Tracer) TraceCreateSchema(ctx context.Context, req *pubsubpb.CreateSchemaRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "CreateSchema", req.GetParent(), opts...)
}

func (tr *Tracer) TraceGetSchema(ctx context.Context, req *pubsubpb.GetSchemaRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "GetSchema", req.GetName(), opts...)
}

func (tr *Tracer) TraceListSchemas(ctx context.Context, req *pubsubpb.ListSchemasRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListSchemas", req.GetParent(), opts...)
}

func (tr *Tracer) TraceListSchemaRevisions(ctx context.Context, req *pubsubpb.ListSchemaRevisionsRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ListSchemaRevisions", req.GetName(), opts...)
}

func (tr *Tracer) TraceCommitSchema(ctx context.Context, req *pubsubpb.CommitSchemaRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "CommitSchema", req.GetName(), opts...)
}

func (tr *Tracer) TraceRollbackSchema(ctx context.Context, req *pubsubpb.RollbackSchemaRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "RollbackSchema", req.GetName(), opts...)
}

func (tr *Tracer) TraceDeleteSchemaRevision(ctx context.Context, req *pubsubpb.DeleteSchemaRevisionRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "DeleteSchemaRevision", req.GetName(), opts...)
}

func (tr *Tracer) TraceDeleteSchema(ctx context.Context, req *pubsubpb.DeleteSchemaRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "DeleteSchema", req.GetName(), opts...)
}

func (tr *Tracer) TraceValidateSchema(ctx context.Context, req *pubsubpb.ValidateSchemaRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ValidateSchema", req.GetParent(), opts...)
}

func (tr *Tracer) TraceValidateMessage(ctx context.Context, req *pubsubpb.ValidateMessageRequest, opts ...Option) (context.Context, func(error)) {
	return tr.TraceAdmin(ctx, "ValidateMessage", req.GetParent(), opts...)
}
