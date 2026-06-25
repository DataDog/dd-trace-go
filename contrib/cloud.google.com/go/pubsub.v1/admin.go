// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package pubsub

import (
	"context"

	vkit "cloud.google.com/go/pubsub/apiv1"
	pubsubpb "cloud.google.com/go/pubsub/apiv1/pubsubpb"
	gax "github.com/googleapis/gax-go/v2"
)

// PublisherClient wraps a *apiv1.PublisherClient, tracing its topic management
// operations. It embeds the wrapped client so any method that is not traced
// (e.g. Publish, IAM helpers, Close) is forwarded unchanged.
type PublisherClient struct {
	*vkit.PublisherClient
	opts []Option
}

// WrapPublisherClient wraps a *apiv1.PublisherClient so that its admin operations
// (CreateTopic, GetTopic, ListTopics, ...) are traced.
func WrapPublisherClient(c *vkit.PublisherClient, opts ...Option) *PublisherClient {
	return &PublisherClient{PublisherClient: c, opts: opts}
}

func (c *PublisherClient) CreateTopic(ctx context.Context, req *pubsubpb.Topic, opts ...gax.CallOption) (*pubsubpb.Topic, error) {
	ctx, finish := pstrace.TraceCreateTopicV1(ctx, req, c.opts...)
	resp, err := c.PublisherClient.CreateTopic(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *PublisherClient) UpdateTopic(ctx context.Context, req *pubsubpb.UpdateTopicRequest, opts ...gax.CallOption) (*pubsubpb.Topic, error) {
	ctx, finish := pstrace.TraceUpdateTopicV1(ctx, req, c.opts...)
	resp, err := c.PublisherClient.UpdateTopic(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *PublisherClient) GetTopic(ctx context.Context, req *pubsubpb.GetTopicRequest, opts ...gax.CallOption) (*pubsubpb.Topic, error) {
	ctx, finish := pstrace.TraceGetTopicV1(ctx, req, c.opts...)
	resp, err := c.PublisherClient.GetTopic(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *PublisherClient) ListTopics(ctx context.Context, req *pubsubpb.ListTopicsRequest, opts ...gax.CallOption) *vkit.TopicIterator {
	ctx, finish := pstrace.TraceListTopicsV1(ctx, req, c.opts...)
	it := c.PublisherClient.ListTopics(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *PublisherClient) ListTopicSubscriptions(ctx context.Context, req *pubsubpb.ListTopicSubscriptionsRequest, opts ...gax.CallOption) *vkit.StringIterator {
	ctx, finish := pstrace.TraceListTopicSubscriptionsV1(ctx, req, c.opts...)
	it := c.PublisherClient.ListTopicSubscriptions(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *PublisherClient) ListTopicSnapshots(ctx context.Context, req *pubsubpb.ListTopicSnapshotsRequest, opts ...gax.CallOption) *vkit.StringIterator {
	ctx, finish := pstrace.TraceListTopicSnapshotsV1(ctx, req, c.opts...)
	it := c.PublisherClient.ListTopicSnapshots(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *PublisherClient) DeleteTopic(ctx context.Context, req *pubsubpb.DeleteTopicRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceDeleteTopicV1(ctx, req, c.opts...)
	err := c.PublisherClient.DeleteTopic(ctx, req, opts...)
	finish(err)
	return err
}

func (c *PublisherClient) DetachSubscription(ctx context.Context, req *pubsubpb.DetachSubscriptionRequest, opts ...gax.CallOption) (*pubsubpb.DetachSubscriptionResponse, error) {
	ctx, finish := pstrace.TraceDetachSubscriptionV1(ctx, req, c.opts...)
	resp, err := c.PublisherClient.DetachSubscription(ctx, req, opts...)
	finish(err)
	return resp, err
}

// SubscriberClient wraps a *apiv1.SubscriberClient, tracing its subscription and
// snapshot management operations. Non-admin RPCs (Pull, Acknowledge, ModifyAckDeadline,
// Close, ...) are forwarded to the embedded client unchanged.
type SubscriberClient struct {
	*vkit.SubscriberClient
	opts []Option
}

// WrapSubscriberClient wraps a *apiv1.SubscriberClient so that its admin operations
// (CreateSubscription, GetSubscription, ModifyPushConfig, Seek, ...) are traced.
func WrapSubscriberClient(c *vkit.SubscriberClient, opts ...Option) *SubscriberClient {
	return &SubscriberClient{SubscriberClient: c, opts: opts}
}

func (c *SubscriberClient) CreateSubscription(ctx context.Context, req *pubsubpb.Subscription, opts ...gax.CallOption) (*pubsubpb.Subscription, error) {
	ctx, finish := pstrace.TraceCreateSubscriptionV1(ctx, req, c.opts...)
	resp, err := c.SubscriberClient.CreateSubscription(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriberClient) GetSubscription(ctx context.Context, req *pubsubpb.GetSubscriptionRequest, opts ...gax.CallOption) (*pubsubpb.Subscription, error) {
	ctx, finish := pstrace.TraceGetSubscriptionV1(ctx, req, c.opts...)
	resp, err := c.SubscriberClient.GetSubscription(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriberClient) UpdateSubscription(ctx context.Context, req *pubsubpb.UpdateSubscriptionRequest, opts ...gax.CallOption) (*pubsubpb.Subscription, error) {
	ctx, finish := pstrace.TraceUpdateSubscriptionV1(ctx, req, c.opts...)
	resp, err := c.SubscriberClient.UpdateSubscription(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriberClient) ListSubscriptions(ctx context.Context, req *pubsubpb.ListSubscriptionsRequest, opts ...gax.CallOption) *vkit.SubscriptionIterator {
	ctx, finish := pstrace.TraceListSubscriptionsV1(ctx, req, c.opts...)
	it := c.SubscriberClient.ListSubscriptions(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *SubscriberClient) DeleteSubscription(ctx context.Context, req *pubsubpb.DeleteSubscriptionRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceDeleteSubscriptionV1(ctx, req, c.opts...)
	err := c.SubscriberClient.DeleteSubscription(ctx, req, opts...)
	finish(err)
	return err
}

func (c *SubscriberClient) ModifyPushConfig(ctx context.Context, req *pubsubpb.ModifyPushConfigRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceModifyPushConfigV1(ctx, req, c.opts...)
	err := c.SubscriberClient.ModifyPushConfig(ctx, req, opts...)
	finish(err)
	return err
}

func (c *SubscriberClient) GetSnapshot(ctx context.Context, req *pubsubpb.GetSnapshotRequest, opts ...gax.CallOption) (*pubsubpb.Snapshot, error) {
	ctx, finish := pstrace.TraceGetSnapshotV1(ctx, req, c.opts...)
	resp, err := c.SubscriberClient.GetSnapshot(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriberClient) ListSnapshots(ctx context.Context, req *pubsubpb.ListSnapshotsRequest, opts ...gax.CallOption) *vkit.SnapshotIterator {
	ctx, finish := pstrace.TraceListSnapshotsV1(ctx, req, c.opts...)
	it := c.SubscriberClient.ListSnapshots(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *SubscriberClient) CreateSnapshot(ctx context.Context, req *pubsubpb.CreateSnapshotRequest, opts ...gax.CallOption) (*pubsubpb.Snapshot, error) {
	ctx, finish := pstrace.TraceCreateSnapshotV1(ctx, req, c.opts...)
	resp, err := c.SubscriberClient.CreateSnapshot(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriberClient) UpdateSnapshot(ctx context.Context, req *pubsubpb.UpdateSnapshotRequest, opts ...gax.CallOption) (*pubsubpb.Snapshot, error) {
	ctx, finish := pstrace.TraceUpdateSnapshotV1(ctx, req, c.opts...)
	resp, err := c.SubscriberClient.UpdateSnapshot(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SubscriberClient) DeleteSnapshot(ctx context.Context, req *pubsubpb.DeleteSnapshotRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceDeleteSnapshotV1(ctx, req, c.opts...)
	err := c.SubscriberClient.DeleteSnapshot(ctx, req, opts...)
	finish(err)
	return err
}

func (c *SubscriberClient) Seek(ctx context.Context, req *pubsubpb.SeekRequest, opts ...gax.CallOption) (*pubsubpb.SeekResponse, error) {
	ctx, finish := pstrace.TraceSeekV1(ctx, req, c.opts...)
	resp, err := c.SubscriberClient.Seek(ctx, req, opts...)
	finish(err)
	return resp, err
}

// SchemaClient wraps a *apiv1.SchemaClient, tracing its schema management operations.
// Non-admin RPCs (IAM helpers, Close) are forwarded unchanged.
type SchemaClient struct {
	*vkit.SchemaClient
	opts []Option
}

// WrapSchemaClient wraps a *apiv1.SchemaClient so that its operations are traced.
func WrapSchemaClient(c *vkit.SchemaClient, opts ...Option) *SchemaClient {
	return &SchemaClient{SchemaClient: c, opts: opts}
}

func (c *SchemaClient) CreateSchema(ctx context.Context, req *pubsubpb.CreateSchemaRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceCreateSchemaV1(ctx, req, c.opts...)
	resp, err := c.SchemaClient.CreateSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) GetSchema(ctx context.Context, req *pubsubpb.GetSchemaRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceGetSchemaV1(ctx, req, c.opts...)
	resp, err := c.SchemaClient.GetSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) ListSchemas(ctx context.Context, req *pubsubpb.ListSchemasRequest, opts ...gax.CallOption) *vkit.SchemaIterator {
	ctx, finish := pstrace.TraceListSchemasV1(ctx, req, c.opts...)
	it := c.SchemaClient.ListSchemas(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *SchemaClient) ListSchemaRevisions(ctx context.Context, req *pubsubpb.ListSchemaRevisionsRequest, opts ...gax.CallOption) *vkit.SchemaIterator {
	ctx, finish := pstrace.TraceListSchemaRevisionsV1(ctx, req, c.opts...)
	it := c.SchemaClient.ListSchemaRevisions(ctx, req, opts...)
	finish(nil)
	return it
}

func (c *SchemaClient) CommitSchema(ctx context.Context, req *pubsubpb.CommitSchemaRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceCommitSchemaV1(ctx, req, c.opts...)
	resp, err := c.SchemaClient.CommitSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) RollbackSchema(ctx context.Context, req *pubsubpb.RollbackSchemaRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceRollbackSchemaV1(ctx, req, c.opts...)
	resp, err := c.SchemaClient.RollbackSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) DeleteSchemaRevision(ctx context.Context, req *pubsubpb.DeleteSchemaRevisionRequest, opts ...gax.CallOption) (*pubsubpb.Schema, error) {
	ctx, finish := pstrace.TraceDeleteSchemaRevisionV1(ctx, req, c.opts...)
	resp, err := c.SchemaClient.DeleteSchemaRevision(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) DeleteSchema(ctx context.Context, req *pubsubpb.DeleteSchemaRequest, opts ...gax.CallOption) error {
	ctx, finish := pstrace.TraceDeleteSchemaV1(ctx, req, c.opts...)
	err := c.SchemaClient.DeleteSchema(ctx, req, opts...)
	finish(err)
	return err
}

func (c *SchemaClient) ValidateSchema(ctx context.Context, req *pubsubpb.ValidateSchemaRequest, opts ...gax.CallOption) (*pubsubpb.ValidateSchemaResponse, error) {
	ctx, finish := pstrace.TraceValidateSchemaV1(ctx, req, c.opts...)
	resp, err := c.SchemaClient.ValidateSchema(ctx, req, opts...)
	finish(err)
	return resp, err
}

func (c *SchemaClient) ValidateMessage(ctx context.Context, req *pubsubpb.ValidateMessageRequest, opts ...gax.CallOption) (*pubsubpb.ValidateMessageResponse, error) {
	ctx, finish := pstrace.TraceValidateMessageV1(ctx, req, c.opts...)
	resp, err := c.SchemaClient.ValidateMessage(ctx, req, opts...)
	finish(err)
	return resp, err
}
