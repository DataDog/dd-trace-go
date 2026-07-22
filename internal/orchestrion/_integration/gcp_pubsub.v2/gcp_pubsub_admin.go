// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

package gcppubsub

import (
	"context"
	"fmt"
	"testing"

	"cloud.google.com/go/pubsub/v2"
	vkit "cloud.google.com/go/pubsub/v2/apiv1"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tclog "github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/modules/gcloud"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/api/option/internaloption"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

const (
	adminTopic        = "admin-topic"
	adminSubscription = "admin-subscription"
	adminSnapshot     = "admin-snapshot"
	adminSchema       = "admin-schema"
	avroDefinition    = `{"type":"record","name":"Test","fields":[{"name":"f","type":"string"}]}`
)

type adminBase struct {
	container *gcloud.GCloudContainer
	uri       string
	projectID string
}

// emulatorOptions returns the client options needed to reach the plaintext Pub/Sub
// emulator by dialing internally. Passing these (rather than a pre-built
// option.WithGRPCConn) lets orchestrion append the admin interceptor to the GAPIC
// client constructors it hooks.
func emulatorOptions(uri string) []option.ClientOption {
	return []option.ClientOption{
		option.WithEndpoint(uri),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		option.WithoutAuthentication(),
		internaloption.SkipDialSettingsValidation(),
	}
}

func (b *adminBase) setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	var err error
	b.container, err = gcloud.RunPubsub(ctx,
		"gcr.io/google.com/cloudsdktool/google-cloud-cli:emulators",
		gcloud.WithProjectID(testProject),
		testcontainers.WithLogger(tclog.TestLogger(t)),
		containers.WithTestLogConsumer(t),
	)
	containers.AssertTestContainersError(t, err)
	containers.RegisterContainerCleanup(t, b.container)

	b.projectID = b.container.Settings.ProjectID
	b.uri = b.container.URI
}

func (b *adminBase) projectPath() string {
	return "projects/" + b.projectID
}

func (b *adminBase) topicPath(id string) string {
	return fmt.Sprintf("projects/%s/topics/%s", b.projectID, id)
}

func (b *adminBase) subscriptionPath(id string) string {
	return fmt.Sprintf("projects/%s/subscriptions/%s", b.projectID, id)
}

func (b *adminBase) snapshotPath(id string) string {
	return fmt.Sprintf("projects/%s/snapshots/%s", b.projectID, id)
}

func (b *adminBase) schemaPath(id string) string {
	return fmt.Sprintf("projects/%s/schemas/%s", b.projectID, id)
}

func drain[T any](t *testing.T, next func() (T, error)) {
	t.Helper()
	for {
		if _, err := next(); err == iterator.Done {
			return
		} else {
			require.NoError(t, err)
		}
	}
}

func (b *adminBase) adminTrace(method, resourcePath string) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "gcp.pubsub.request",
			"type":     "worker",
			"resource": method + " " + resourcePath,
			"service":  "gcp_pubsub.v2.test",
		},
		Meta: map[string]string{
			"span.kind":         "client",
			"component":         "cloud.google.com/go/pubsub.v2",
			"messaging.system":  "googlepubsub",
			"pubsub.method":     method,
			ext.GCPProjectID: testProject,
		},
	}
}

func (b *adminBase) adminErrorTrace(method, resourcePath, errMsg string) *trace.Trace {
	tr := b.adminTrace(method, resourcePath)
	tr.Meta["error.message"] = errMsg
	return tr
}

// TestCaseAdminClient exercises admin ops via admin clients exposed by the
// high-level pubsub.Client. These are constructed internally by pubsub.NewClient
// (a variadic-spread call).
type TestCaseAdminClient struct {
	adminBase
	client *pubsub.Client
}

func (tc *TestCaseAdminClient) Setup(ctx context.Context, t *testing.T) {
	tc.setup(ctx, t)

	var err error
	tc.client, err = pubsub.NewClient(ctx, tc.projectID, emulatorOptions(tc.uri)...)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, tc.client.Close()) })
}

func (tc *TestCaseAdminClient) Run(ctx context.Context, t *testing.T) {
	topic, err := tc.client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{
		Name: tc.topicPath(adminTopic),
	})
	require.NoError(t, err)

	_, err = tc.client.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name:  tc.subscriptionPath(adminSubscription),
		Topic: topic.Name,
	})
	require.NoError(t, err)

	drain(t, tc.client.TopicAdminClient.ListTopics(ctx, &pubsubpb.ListTopicsRequest{
		Project: tc.projectPath(),
	}).Next)
	drain(t, tc.client.SubscriptionAdminClient.ListSubscriptions(ctx, &pubsubpb.ListSubscriptionsRequest{
		Project: tc.projectPath(),
	}).Next)

	_, err = tc.client.SubscriptionAdminClient.CreateSnapshot(ctx, &pubsubpb.CreateSnapshotRequest{
		Name:         tc.snapshotPath(adminSnapshot),
		Subscription: tc.subscriptionPath(adminSubscription),
	})
	require.NoError(t, err)

	drain(t, tc.client.SubscriptionAdminClient.ListSnapshots(ctx, &pubsubpb.ListSnapshotsRequest{
		Project: tc.projectPath(),
	}).Next)

	err = tc.client.SubscriptionAdminClient.DeleteSnapshot(ctx, &pubsubpb.DeleteSnapshotRequest{
		Snapshot: tc.snapshotPath(adminSnapshot),
	})
	require.NoError(t, err)

	err = tc.client.SubscriptionAdminClient.DeleteSubscription(ctx, &pubsubpb.DeleteSubscriptionRequest{
		Subscription: tc.subscriptionPath(adminSubscription),
	})
	require.NoError(t, err)

	err = tc.client.TopicAdminClient.DeleteTopic(ctx, &pubsubpb.DeleteTopicRequest{
		Topic: tc.topicPath(adminTopic),
	})
	require.NoError(t, err)
}

func (tc *TestCaseAdminClient) ExpectedTraces() trace.Traces {
	return trace.Traces{
		tc.adminTrace("CreateTopic", tc.topicPath(adminTopic)),
		tc.adminTrace("CreateSubscription", tc.subscriptionPath(adminSubscription)),
		tc.adminTrace("ListTopics", tc.projectPath()),
		tc.adminTrace("ListSubscriptions", tc.projectPath()),
		tc.adminTrace("CreateSnapshot", tc.snapshotPath(adminSnapshot)),
		tc.adminTrace("ListSnapshots", tc.projectPath()),
		tc.adminTrace("DeleteSnapshot", tc.snapshotPath(adminSnapshot)),
		tc.adminTrace("DeleteSubscription", tc.subscriptionPath(adminSubscription)),
		tc.adminTrace("DeleteTopic", tc.topicPath(adminTopic)),
	}
}

// TestCaseAdminGAPIC exercises admin ops via a directly-constructed GAPIC
// TopicAdminClient.
type TestCaseAdminGAPIC struct {
	adminBase
	missingErrMsg string
}

func (tc *TestCaseAdminGAPIC) Setup(ctx context.Context, t *testing.T) {
	tc.setup(ctx, t)

	// Fixture topic is created before the tracer starts.
	client, err := pubsub.NewClient(ctx, tc.projectID, emulatorOptions(tc.uri)...)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })

	_, err = client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{
		Name: tc.topicPath(adminTopic),
	})
	require.NoError(t, err)
}

func (tc *TestCaseAdminGAPIC) Run(ctx context.Context, t *testing.T) {
	tac, err := vkit.NewTopicAdminClient(ctx, emulatorOptions(tc.uri)...)
	require.NoError(t, err)
	defer func() { assert.NoError(t, tac.Close()) }()

	_, err = tac.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: tc.topicPath(adminTopic)})
	require.NoError(t, err)

	_, err = tac.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: tc.topicPath("missing")})
	require.Error(t, err)
	tc.missingErrMsg = err.Error()
}

func (tc *TestCaseAdminGAPIC) ExpectedTraces() trace.Traces {
	return trace.Traces{
		tc.adminTrace("GetTopic", tc.topicPath(adminTopic)),
		tc.adminErrorTrace("GetTopic", tc.topicPath("missing"), tc.missingErrMsg),
	}
}

// TestCaseAdminSchema exercises admin ops via a directly-constructed GAPIC
// SchemaClient (not exposed on pubsub.Client).
type TestCaseAdminSchema struct {
	adminBase
}

func (tc *TestCaseAdminSchema) Setup(ctx context.Context, t *testing.T) {
	tc.setup(ctx, t)
}

func (tc *TestCaseAdminSchema) Run(ctx context.Context, t *testing.T) {
	schemaClient, err := vkit.NewSchemaClient(ctx, emulatorOptions(tc.uri)...)
	require.NoError(t, err)
	defer func() { assert.NoError(t, schemaClient.Close()) }()

	_, err = schemaClient.CreateSchema(ctx, &pubsubpb.CreateSchemaRequest{
		Parent: tc.projectPath(),
		Schema: &pubsubpb.Schema{
			Type:       pubsubpb.Schema_AVRO,
			Definition: avroDefinition,
		},
		SchemaId: adminSchema,
	})
	require.NoError(t, err)

	_, err = schemaClient.GetSchema(ctx, &pubsubpb.GetSchemaRequest{Name: tc.schemaPath(adminSchema)})
	require.NoError(t, err)

	drain(t, schemaClient.ListSchemas(ctx, &pubsubpb.ListSchemasRequest{Parent: tc.projectPath()}).Next)

	err = schemaClient.DeleteSchema(ctx, &pubsubpb.DeleteSchemaRequest{Name: tc.schemaPath(adminSchema)})
	require.NoError(t, err)
}

func (tc *TestCaseAdminSchema) ExpectedTraces() trace.Traces {
	return trace.Traces{
		tc.adminTrace("CreateSchema", tc.projectPath()),
		tc.adminTrace("GetSchema", tc.schemaPath(adminSchema)),
		tc.adminTrace("ListSchemas", tc.projectPath()),
		tc.adminTrace("DeleteSchema", tc.schemaPath(adminSchema)),
	}
}
