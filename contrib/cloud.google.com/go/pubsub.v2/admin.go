// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package pubsub

import (
	"context"

	vkit "cloud.google.com/go/pubsub/v2/apiv1"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	gax "github.com/googleapis/gax-go/v2"
)

// TopicAdminClient wraps *apiv1.TopicAdminClient, tracing its topic and snapshot
// management operations. It embeds the wrapped client, so any method that is not traced
// (e.g. Publish, IAM helpers, Close) is forwarded to the underlying client unchanged.
type TopicAdminClient struct {
	*vkit.TopicAdminClient
	opts []Option
}

// WrapTopicAdminClient wraps *apiv1.TopicAdminClient so that its admin operations are traced.
func WrapTopicAdminClient(c *vkit.TopicAdminClient, opts ...Option) *TopicAdminClient {
	return &TopicAdminClient{TopicAdminClient: c, opts: opts}
}

func (c *TopicAdminClient) CreateTopic(ctx context.Context, req *pubsubpb.Topic, opts ...gax.CallOption) (*pubsubpb.Topic, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "createTopic", req.GetName(), c.opts...)
	resp, err := c.TopicAdminClient.CreateTopic(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *TopicAdminClient) UpdateTopic(ctx context.Context, req *pubsubpb.UpdateTopicRequest, opts ...gax.CallOption) (*pubsubpb.Topic, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "updateTopic", req.GetTopic().GetName(), c.opts...)
	resp, err := c.TopicAdminClient.UpdateTopic(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *TopicAdminClient) GetTopic(ctx context.Context, req *pubsubpb.GetTopicRequest, opts ...gax.CallOption) (*pubsubpb.Topic, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "getTopic", req.GetTopic(), c.opts...)
	resp, err := c.TopicAdminClient.GetTopic(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *TopicAdminClient) ListTopics(ctx context.Context, req *pubsubpb.ListTopicsRequest, opts ...gax.CallOption) *vkit.TopicIterator {
	ctx, finish := pstrace.TraceAdmin(ctx, "listTopics", req.GetProject(), c.opts...)
	it := c.TopicAdminClient.ListTopics(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *TopicAdminClient) ListTopicSubscriptions(ctx context.Context, req *pubsubpb.ListTopicSubscriptionsRequest, opts ...gax.CallOption) *vkit.StringIterator {
	ctx, finish := pstrace.TraceAdmin(ctx, "listTopicSubscriptions", req.GetTopic(), c.opts...)
	it := c.TopicAdminClient.ListTopicSubscriptions(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *TopicAdminClient) ListTopicSnapshots(ctx context.Context, req *pubsubpb.ListTopicSnapshotsRequest, opts ...gax.CallOption) *vkit.StringIterator {
	ctx, finish := pstrace.TraceAdmin(ctx, "listTopicSnapshots", req.GetTopic(), c.opts...)
	it := c.TopicAdminClient.ListTopicSnapshots(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *TopicAdminClient) DeleteTopic(ctx context.Context, req *pubsubpb.DeleteTopicRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceAdmin(ctx, "deleteTopic", req.GetTopic(), c.opts...)
	err := c.TopicAdminClient.DeleteTopic(ctx, req, opts...)
	finish(err)
	return err
}

func (c *TopicAdminClient) DetachSubscription(ctx context.Context, req *pubsubpb.DetachSubscriptionRequest, opts ...gax.CallOption) (*pubsubpb.DetachSubscriptionResponse, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "detachSubscription", req.GetSubscription(), c.opts...)
	resp, err := c.TopicAdminClient.DetachSubscription(ctx, req, opts...)
	finish(err)
	return resp, err
}

// SubscriptionAdminClient wraps a *apiv1.SubscriptionAdminClient, tracing its subscription
// and snapshot management operations. It embeds the wrapped client, so any method that is not
// traced (e.g. Pull, Acknowledge, ModifyAckDeadline, Close) is forwarded unchanged.
type SubscriptionAdminClient struct {
	*vkit.SubscriptionAdminClient
	opts []Option
}

// WrapSubscriptionAdminClient wraps a *apiv1.SubscriptionAdminClient (commonly obtained from
// (*pubsub.Client).SubscriptionAdminClient) so that its admin operations are traced.
func WrapSubscriptionAdminClient(c *vkit.SubscriptionAdminClient, opts ...Option) *SubscriptionAdminClient {
	return &SubscriptionAdminClient{SubscriptionAdminClient: c, opts: opts}
}

func (c *SubscriptionAdminClient) CreateSubscription(ctx context.Context, req *pubsubpb.Subscription, opts ...gax.CallOption) (*pubsubpb.Subscription, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "createSubscription", req.GetName(), c.opts...)
	resp, err := c.SubscriptionAdminClient.CreateSubscription(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriptionAdminClient) GetSubscription(ctx context.Context, req *pubsubpb.GetSubscriptionRequest, opts ...gax.CallOption) (*pubsubpb.Subscription, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "getSubscription", req.GetSubscription(), c.opts...)
	resp, err := c.SubscriptionAdminClient.GetSubscription(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriptionAdminClient) UpdateSubscription(ctx context.Context, req *pubsubpb.UpdateSubscriptionRequest, opts ...gax.CallOption) (*pubsubpb.Subscription, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "updateSubscription", req.GetSubscription().GetName(), c.opts...)
	resp, err := c.SubscriptionAdminClient.UpdateSubscription(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriptionAdminClient) ListSubscriptions(ctx context.Context, req *pubsubpb.ListSubscriptionsRequest, opts ...gax.CallOption) *vkit.SubscriptionIterator {
	ctx, finish := pstrace.TraceAdmin(ctx, "listSubscriptions", req.GetProject(), c.opts...)
	it := c.SubscriptionAdminClient.ListSubscriptions(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *SubscriptionAdminClient) DeleteSubscription(ctx context.Context, req *pubsubpb.DeleteSubscriptionRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceAdmin(ctx, "deleteSubscription", req.GetSubscription(), c.opts...)
	err := c.SubscriptionAdminClient.DeleteSubscription(ctx, req, opts...)
	finish(err)
	return err
}

func (c *SubscriptionAdminClient) ModifyPushConfig(ctx context.Context, req *pubsubpb.ModifyPushConfigRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceAdmin(ctx, "modifyPushConfig", req.GetSubscription(), c.opts...)
	err := c.SubscriptionAdminClient.ModifyPushConfig(ctx, req, opts...)
	finish(err)
	return err
}

func (c *SubscriptionAdminClient) GetSnapshot(ctx context.Context, req *pubsubpb.GetSnapshotRequest, opts ...gax.CallOption) (*pubsubpb.Snapshot, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "getSnapshot", req.GetSnapshot(), c.opts...)
	resp, err := c.SubscriptionAdminClient.GetSnapshot(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriptionAdminClient) ListSnapshots(ctx context.Context, req *pubsubpb.ListSnapshotsRequest, opts ...gax.CallOption) *vkit.SnapshotIterator {
	ctx, finish := pstrace.TraceAdmin(ctx, "listSnapshots", req.GetProject(), c.opts...)
	it := c.SubscriptionAdminClient.ListSnapshots(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *SubscriptionAdminClient) CreateSnapshot(ctx context.Context, req *pubsubpb.CreateSnapshotRequest, opts ...gax.CallOption) (*pubsubpb.Snapshot, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "createSnapshot", req.GetName(), c.opts...)
	resp, err := c.SubscriptionAdminClient.CreateSnapshot(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriptionAdminClient) UpdateSnapshot(ctx context.Context, req *pubsubpb.UpdateSnapshotRequest, opts ...gax.CallOption) (*pubsubpb.Snapshot, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "updateSnapshot", req.GetSnapshot().GetName(), c.opts...)
	resp, err := c.SubscriptionAdminClient.UpdateSnapshot(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriptionAdminClient) DeleteSnapshot(ctx context.Context, req *pubsubpb.DeleteSnapshotRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceAdmin(ctx, "deleteSnapshot", req.GetSnapshot(), c.opts...)
	err := c.SubscriptionAdminClient.DeleteSnapshot(ctx, req, opts...)
	finish(err)
	return err
}

func (c *SubscriptionAdminClient) Seek(ctx context.Context, req *pubsubpb.SeekRequest, opts ...gax.CallOption) (*pubsubpb.SeekResponse, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "seek", req.GetSubscription(), c.opts...)
	resp, err := c.SubscriptionAdminClient.Seek(ctx, req, opts...)
	finish(err)
	return resp, err
}

// SchemaClient wraps *apiv1.SchemaClient, tracing its schema management operations.
// It embeds the wrapped client, so any method that is not traced (e.g. IAM helpers, Close)
// is forwarded to the underlying client unchanged.
type SchemaClient struct {
	*vkit.SchemaClient
	opts []Option
}

func WrapSchemaClient(c *vkit.SchemaClient, opts ...Option) *SchemaClient {
	return &SchemaClient{SchemaClient: c, opts: opts}
}

func (c *SchemaClient) CreateSchema(ctx context.Context, req *pubsubpb.CreateSchemaRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "createSchema", req.GetParent(), c.opts...)
	resp, err := c.SchemaClient.CreateSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) GetSchema(ctx context.Context, req *pubsubpb.GetSchemaRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "getSchema", req.GetName(), c.opts...)
	resp, err := c.SchemaClient.GetSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) ListSchemas(ctx context.Context, req *pubsubpb.ListSchemasRequest, opts ...gax.CallOption) *vkit.SchemaIterator {
	ctx, finish := pstrace.TraceAdmin(ctx, "listSchemas", req.GetParent(), c.opts...)
	it := c.SchemaClient.ListSchemas(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *SchemaClient) ListSchemaRevisions(ctx context.Context, req *pubsubpb.ListSchemaRevisionsRequest, opts ...gax.CallOption) *vkit.SchemaIterator {
	ctx, finish := pstrace.TraceAdmin(ctx, "listSchemaRevisions", req.GetName(), c.opts...)
	it := c.SchemaClient.ListSchemaRevisions(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *SchemaClient) CommitSchema(ctx context.Context, req *pubsubpb.CommitSchemaRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "commitSchema", req.GetName(), c.opts...)
	resp, err := c.SchemaClient.CommitSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) RollbackSchema(ctx context.Context, req *pubsubpb.RollbackSchemaRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "rollbackSchema", req.GetName(), c.opts...)
	resp, err := c.SchemaClient.RollbackSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) DeleteSchemaRevision(ctx context.Context, req *pubsubpb.DeleteSchemaRevisionRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "deleteSchemaRevision", req.GetName(), c.opts...)
	resp, err := c.SchemaClient.DeleteSchemaRevision(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) DeleteSchema(ctx context.Context, req *pubsubpb.DeleteSchemaRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceAdmin(ctx, "deleteSchema", req.GetName(), c.opts...)
	err := c.SchemaClient.DeleteSchema(ctx, req, opts...)
	finish(err)
	return err
}

func (c *SchemaClient) ValidateSchema(ctx context.Context, req *pubsubpb.ValidateSchemaRequest, opts ...gax.CallOption) (*pubsubpb.ValidateSchemaResponse, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "validateSchema", req.GetParent(), c.opts...)
	resp, err := c.SchemaClient.ValidateSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) ValidateMessage(ctx context.Context, req *pubsubpb.ValidateMessageRequest, opts ...gax.CallOption) (*pubsubpb.ValidateMessageResponse, error) {
	ctx, finish := pstrace.TraceAdmin(ctx, "validateMessage", req.GetParent(), c.opts...)
	resp, err := c.SchemaClient.ValidateMessage(ctx, req, opts...)
	finish(err)
	return resp, err
}
