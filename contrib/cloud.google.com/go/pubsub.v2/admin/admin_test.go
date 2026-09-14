// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package admin

import (
	"testing"

	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"github.com/stretchr/testify/assert"
)

func TestResolveAdminResource(t *testing.T) {
	tests := []struct {
		name string
		req  any
		want string
		ok   bool
	}{
		// TopicAdminClient
		{"Topic", &pubsubpb.Topic{Name: "projects/p/topics/t"}, "projects/p/topics/t", true},
		{"UpdateTopic", &pubsubpb.UpdateTopicRequest{Topic: &pubsubpb.Topic{Name: "projects/p/topics/t"}}, "projects/p/topics/t", true},
		{"GetTopic", &pubsubpb.GetTopicRequest{Topic: "projects/p/topics/t"}, "projects/p/topics/t", true},
		{"ListTopics", &pubsubpb.ListTopicsRequest{Project: "projects/p"}, "projects/p", true},
		{"ListTopicSubscriptions", &pubsubpb.ListTopicSubscriptionsRequest{Topic: "projects/p/topics/t"}, "projects/p/topics/t", true},
		{"ListTopicSnapshots", &pubsubpb.ListTopicSnapshotsRequest{Topic: "projects/p/topics/t"}, "projects/p/topics/t", true},
		{"DeleteTopic", &pubsubpb.DeleteTopicRequest{Topic: "projects/p/topics/t"}, "projects/p/topics/t", true},
		{"DetachSubscription", &pubsubpb.DetachSubscriptionRequest{Subscription: "projects/p/subscriptions/s"}, "projects/p/subscriptions/s", true},

		// SubscriptionAdminClient
		{"Subscription", &pubsubpb.Subscription{Name: "projects/p/subscriptions/s"}, "projects/p/subscriptions/s", true},
		{"GetSubscription", &pubsubpb.GetSubscriptionRequest{Subscription: "projects/p/subscriptions/s"}, "projects/p/subscriptions/s", true},
		{"UpdateSubscription", &pubsubpb.UpdateSubscriptionRequest{Subscription: &pubsubpb.Subscription{Name: "projects/p/subscriptions/s"}}, "projects/p/subscriptions/s", true},
		{"ListSubscriptions", &pubsubpb.ListSubscriptionsRequest{Project: "projects/p"}, "projects/p", true},
		{"DeleteSubscription", &pubsubpb.DeleteSubscriptionRequest{Subscription: "projects/p/subscriptions/s"}, "projects/p/subscriptions/s", true},
		{"ModifyPushConfig", &pubsubpb.ModifyPushConfigRequest{Subscription: "projects/p/subscriptions/s"}, "projects/p/subscriptions/s", true},
		{"GetSnapshot", &pubsubpb.GetSnapshotRequest{Snapshot: "projects/p/snapshots/sn"}, "projects/p/snapshots/sn", true},
		{"ListSnapshots", &pubsubpb.ListSnapshotsRequest{Project: "projects/p"}, "projects/p", true},
		{"CreateSnapshot", &pubsubpb.CreateSnapshotRequest{Name: "projects/p/snapshots/sn"}, "projects/p/snapshots/sn", true},
		{"UpdateSnapshot", &pubsubpb.UpdateSnapshotRequest{Snapshot: &pubsubpb.Snapshot{Name: "projects/p/snapshots/sn"}}, "projects/p/snapshots/sn", true},
		{"DeleteSnapshot", &pubsubpb.DeleteSnapshotRequest{Snapshot: "projects/p/snapshots/sn"}, "projects/p/snapshots/sn", true},
		{"Seek", &pubsubpb.SeekRequest{Subscription: "projects/p/subscriptions/s"}, "projects/p/subscriptions/s", true},

		// SchemaClient
		{"CreateSchema", &pubsubpb.CreateSchemaRequest{Parent: "projects/p"}, "projects/p", true},
		{"GetSchema", &pubsubpb.GetSchemaRequest{Name: "projects/p/schemas/sc"}, "projects/p/schemas/sc", true},
		{"ListSchemas", &pubsubpb.ListSchemasRequest{Parent: "projects/p"}, "projects/p", true},
		{"ListSchemaRevisions", &pubsubpb.ListSchemaRevisionsRequest{Name: "projects/p/schemas/sc"}, "projects/p/schemas/sc", true},
		{"CommitSchema", &pubsubpb.CommitSchemaRequest{Name: "projects/p/schemas/sc"}, "projects/p/schemas/sc", true},
		{"RollbackSchema", &pubsubpb.RollbackSchemaRequest{Name: "projects/p/schemas/sc"}, "projects/p/schemas/sc", true},
		{"DeleteSchemaRevision", &pubsubpb.DeleteSchemaRevisionRequest{Name: "projects/p/schemas/sc@1"}, "projects/p/schemas/sc@1", true},
		{"DeleteSchema", &pubsubpb.DeleteSchemaRequest{Name: "projects/p/schemas/sc"}, "projects/p/schemas/sc", true},
		{"ValidateSchema", &pubsubpb.ValidateSchemaRequest{Parent: "projects/p"}, "projects/p", true},
		{"ValidateMessage", &pubsubpb.ValidateMessageRequest{Parent: "projects/p"}, "projects/p", true},

		// Non-admin / data-plane RPCs on the same connection
		{"Publish", &pubsubpb.PublishRequest{}, "", false},
		{"Pull", &pubsubpb.PullRequest{}, "", false},
		{"Acknowledge", &pubsubpb.AcknowledgeRequest{}, "", false},
		{"nil", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := resolveAdminResource(tt.req)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAdminMethodName(t *testing.T) {
	assert.Equal(t, "CreateTopic", adminMethodName("/google.pubsub.v1.Publisher/CreateTopic"))
	assert.Equal(t, "CreateTopic", adminMethodName("CreateTopic"))
}
