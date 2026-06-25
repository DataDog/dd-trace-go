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

// TopicAdminClient wraps a *apiv1.TopicAdminClient, tracing its topic and snapshot
// management operations. It embeds the wrapped client, so any method that is not traced
// (e.g. Publish, IAM helpers, Close) is forwarded to the underlying client unchanged.
type TopicAdminClient struct {
	*vkit.TopicAdminClient
	opts []Option
}

// WrapTopicAdminClient wraps a *apiv1.TopicAdminClient (commonly obtained from
// (*pubsub.Client).TopicAdminClient) so that its admin operations are traced.
func WrapTopicAdminClient(c *vkit.TopicAdminClient, opts ...Option) *TopicAdminClient {
	return &TopicAdminClient{TopicAdminClient: c, opts: opts}
}

func (c *TopicAdminClient) CreateTopic(ctx context.Context, req *pubsubpb.Topic, opts ...gax.CallOption) (*pubsubpb.Topic, error) {
	ctx, finish := pstrace.TraceCreateTopic(ctx, req, c.opts...)
	resp, err := c.TopicAdminClient.CreateTopic(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *TopicAdminClient) UpdateTopic(ctx context.Context, req *pubsubpb.UpdateTopicRequest, opts ...gax.CallOption) (*pubsubpb.Topic, error) {
	ctx, finish := pstrace.TraceUpdateTopic(ctx, req, c.opts...)
	resp, err := c.TopicAdminClient.UpdateTopic(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *TopicAdminClient) GetTopic(ctx context.Context, req *pubsubpb.GetTopicRequest, opts ...gax.CallOption) (*pubsubpb.Topic, error) {
	ctx, finish := pstrace.TraceGetTopic(ctx, req, c.opts...)
	resp, err := c.TopicAdminClient.GetTopic(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *TopicAdminClient) ListTopics(ctx context.Context, req *pubsubpb.ListTopicsRequest, opts ...gax.CallOption) *vkit.TopicIterator {
	ctx, finish := pstrace.TraceListTopics(ctx, req, c.opts...)
	it := c.TopicAdminClient.ListTopics(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *TopicAdminClient) ListTopicSubscriptions(ctx context.Context, req *pubsubpb.ListTopicSubscriptionsRequest, opts ...gax.CallOption) *vkit.StringIterator {
	ctx, finish := pstrace.TraceListTopicSubscriptions(ctx, req, c.opts...)
	it := c.TopicAdminClient.ListTopicSubscriptions(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *TopicAdminClient) ListTopicSnapshots(ctx context.Context, req *pubsubpb.ListTopicSnapshotsRequest, opts ...gax.CallOption) *vkit.StringIterator {
	ctx, finish := pstrace.TraceListTopicSnapshots(ctx, req, c.opts...)
	it := c.TopicAdminClient.ListTopicSnapshots(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *TopicAdminClient) DeleteTopic(ctx context.Context, req *pubsubpb.DeleteTopicRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceDeleteTopic(ctx, req, c.opts...)
	err := c.TopicAdminClient.DeleteTopic(ctx, req, opts...)
	finish(err)
	return err
}

func (c *TopicAdminClient) DetachSubscription(ctx context.Context, req *pubsubpb.DetachSubscriptionRequest, opts ...gax.CallOption) (*pubsubpb.DetachSubscriptionResponse, error) {
	ctx, finish := pstrace.TraceDetachSubscription(ctx, req, c.opts...)
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

// CreateSubscription traces a call to the wrapped client's CreateSubscription.
func (c *SubscriptionAdminClient) CreateSubscription(ctx context.Context, req *pubsubpb.Subscription, opts ...gax.CallOption) (*pubsubpb.Subscription, error) {
	ctx, finish := pstrace.TraceCreateSubscription(ctx, req, c.opts...)
	resp, err := c.SubscriptionAdminClient.CreateSubscription(ctx, req, opts...)
	finish(err)
	return resp, err
}

// GetSubscription traces a call to the wrapped client's GetSubscription.
func (c *SubscriptionAdminClient) GetSubscription(ctx context.Context, req *pubsubpb.GetSubscriptionRequest, opts ...gax.CallOption) (*pubsubpb.Subscription, error) {
	ctx, finish := pstrace.TraceGetSubscription(ctx, req, c.opts...)
	resp, err := c.SubscriptionAdminClient.GetSubscription(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriptionAdminClient) UpdateSubscription(ctx context.Context, req *pubsubpb.UpdateSubscriptionRequest, opts ...gax.CallOption) (*pubsubpb.Subscription, error) {
	ctx, finish := pstrace.TraceUpdateSubscription(ctx, req, c.opts...)
	resp, err := c.SubscriptionAdminClient.UpdateSubscription(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriptionAdminClient) ListSubscriptions(ctx context.Context, req *pubsubpb.ListSubscriptionsRequest, opts ...gax.CallOption) *vkit.SubscriptionIterator {
	ctx, finish := pstrace.TraceListSubscriptions(ctx, req, c.opts...)
	it := c.SubscriptionAdminClient.ListSubscriptions(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *SubscriptionAdminClient) DeleteSubscription(ctx context.Context, req *pubsubpb.DeleteSubscriptionRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceDeleteSubscription(ctx, req, c.opts...)
	err := c.SubscriptionAdminClient.DeleteSubscription(ctx, req, opts...)
	finish(err)
	return err
}

func (c *SubscriptionAdminClient) ModifyPushConfig(ctx context.Context, req *pubsubpb.ModifyPushConfigRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceModifyPushConfig(ctx, req, c.opts...)
	err := c.SubscriptionAdminClient.ModifyPushConfig(ctx, req, opts...)
	finish(err)
	return err
}

func (c *SubscriptionAdminClient) GetSnapshot(ctx context.Context, req *pubsubpb.GetSnapshotRequest, opts ...gax.CallOption) (*pubsubpb.Snapshot, error) {
	ctx, finish := pstrace.TraceGetSnapshot(ctx, req, c.opts...)
	resp, err := c.SubscriptionAdminClient.GetSnapshot(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriptionAdminClient) ListSnapshots(ctx context.Context, req *pubsubpb.ListSnapshotsRequest, opts ...gax.CallOption) *vkit.SnapshotIterator {
	ctx, finish := pstrace.TraceListSnapshots(ctx, req, c.opts...)
	it := c.SubscriptionAdminClient.ListSnapshots(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *SubscriptionAdminClient) CreateSnapshot(ctx context.Context, req *pubsubpb.CreateSnapshotRequest, opts ...gax.CallOption) (*pubsubpb.Snapshot, error) {
	ctx, finish := pstrace.TraceCreateSnapshot(ctx, req, c.opts...)
	resp, err := c.SubscriptionAdminClient.CreateSnapshot(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriptionAdminClient) UpdateSnapshot(ctx context.Context, req *pubsubpb.UpdateSnapshotRequest, opts ...gax.CallOption) (*pubsubpb.Snapshot, error) {
	ctx, finish := pstrace.TraceUpdateSnapshot(ctx, req, c.opts...)
	resp, err := c.SubscriptionAdminClient.UpdateSnapshot(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriptionAdminClient) DeleteSnapshot(ctx context.Context, req *pubsubpb.DeleteSnapshotRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceDeleteSnapshot(ctx, req, c.opts...)
	err := c.SubscriptionAdminClient.DeleteSnapshot(ctx, req, opts...)
	finish(err)
	return err
}

func (c *SubscriptionAdminClient) Seek(ctx context.Context, req *pubsubpb.SeekRequest, opts ...gax.CallOption) (*pubsubpb.SeekResponse, error) {
	ctx, finish := pstrace.TraceSeek(ctx, req, c.opts...)
	resp, err := c.SubscriptionAdminClient.Seek(ctx, req, opts...)
	finish(err)
	return resp, err
}

// SchemaClient wraps a *apiv1.SchemaClient, tracing its schema management operations.
// It embeds the wrapped client, so any method that is not traced (e.g. IAM helpers, Close)
// is forwarded to the underlying client unchanged.
type SchemaClient struct {
	*vkit.SchemaClient
	opts []Option
}

// WrapSchemaClient wraps a *apiv1.SchemaClient so that its operations are traced.
func WrapSchemaClient(c *vkit.SchemaClient, opts ...Option) *SchemaClient {
	return &SchemaClient{SchemaClient: c, opts: opts}
}

func (c *SchemaClient) CreateSchema(ctx context.Context, req *pubsubpb.CreateSchemaRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceCreateSchema(ctx, req, c.opts...)
	resp, err := c.SchemaClient.CreateSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) GetSchema(ctx context.Context, req *pubsubpb.GetSchemaRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceGetSchema(ctx, req, c.opts...)
	resp, err := c.SchemaClient.GetSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

// ListSchemas traces a call to the wrapped client's ListSchemas.
func (c *SchemaClient) ListSchemas(ctx context.Context, req *pubsubpb.ListSchemasRequest, opts ...gax.CallOption) *vkit.SchemaIterator {
	ctx, finish := pstrace.TraceListSchemas(ctx, req, c.opts...)
	it := c.SchemaClient.ListSchemas(ctx, req, opts...)
	finish(nil)
	return it
}

// ListSchemaRevisions traces a call to the wrapped client's ListSchemaRevisions.
func (c *SchemaClient) ListSchemaRevisions(ctx context.Context, req *pubsubpb.ListSchemaRevisionsRequest, opts ...gax.CallOption) *vkit.SchemaIterator {
	ctx, finish := pstrace.TraceListSchemaRevisions(ctx, req, c.opts...)
	it := c.SchemaClient.ListSchemaRevisions(ctx, req, opts...)
	finish(nil)
	return it
}

// CommitSchema traces a call to the wrapped client's CommitSchema.
func (c *SchemaClient) CommitSchema(ctx context.Context, req *pubsubpb.CommitSchemaRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceCommitSchema(ctx, req, c.opts...)
	resp, err := c.SchemaClient.CommitSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

// RollbackSchema traces a call to the wrapped client's RollbackSchema.
func (c *SchemaClient) RollbackSchema(ctx context.Context, req *pubsubpb.RollbackSchemaRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceRollbackSchema(ctx, req, c.opts...)
	resp, err := c.SchemaClient.RollbackSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

// DeleteSchemaRevision traces a call to the wrapped client's DeleteSchemaRevision.
func (c *SchemaClient) DeleteSchemaRevision(ctx context.Context, req *pubsubpb.DeleteSchemaRevisionRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceDeleteSchemaRevision(ctx, req, c.opts...)
	resp, err := c.SchemaClient.DeleteSchemaRevision(ctx, req, opts...)
	finish(err)
	return resp, err
}

// DeleteSchema traces a call to the wrapped client's DeleteSchema.
func (c *SchemaClient) DeleteSchema(ctx context.Context, req *pubsubpb.DeleteSchemaRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceDeleteSchema(ctx, req, c.opts...)
	err := c.SchemaClient.DeleteSchema(ctx, req, opts...)
	finish(err)
	return err
}

// ValidateSchema traces a call to the wrapped client's ValidateSchema.
func (c *SchemaClient) ValidateSchema(ctx context.Context, req *pubsubpb.ValidateSchemaRequest, opts ...gax.CallOption) (*pubsubpb.ValidateSchemaResponse, error) {
	ctx, finish := pstrace.TraceValidateSchema(ctx, req, c.opts...)
	resp, err := c.SchemaClient.ValidateSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

// ValidateMessage traces a call to the wrapped client's ValidateMessage.
func (c *SchemaClient) ValidateMessage(ctx context.Context, req *pubsubpb.ValidateMessageRequest, opts ...gax.CallOption) (*pubsubpb.ValidateMessageResponse, error) {
	ctx, finish := pstrace.TraceValidateMessage(ctx, req, c.opts...)
	resp, err := c.SchemaClient.ValidateMessage(ctx, req, opts...)
	finish(err)
	return resp, err
}
