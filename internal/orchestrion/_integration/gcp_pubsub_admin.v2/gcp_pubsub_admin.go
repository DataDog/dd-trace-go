// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

// Package gcppubsubadmin exercises orchestrion tracing of Pub/Sub v2 admin
// operations via high-level and GAPIC clients dialing the emulator themselves.
package gcppubsubadmin

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
	testProject      = "pstest-orchestrion"
	testTopic        = "admin-topic"
	testSubscription = "admin-subscription"
	testSnapshot     = "admin-snapshot"
	testSchema       = "admin-schema"
	avroDefinition   = `{"type":"record","name":"Test","fields":[{"name":"f","type":"string"}]}`
)

type TestCase struct {
	container     *gcloud.GCloudContainer
	client        *pubsub.Client
	uri           string
	projectID     string
	missingErrMsg string
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

func (tc *TestCase) projectPath() string {
	return fmt.Sprintf("projects/%s", tc.projectID)
}

func (tc *TestCase) topicPath(id string) string {
	return fmt.Sprintf("projects/%s/topics/%s", tc.projectID, id)
}

func (tc *TestCase) subscriptionPath(id string) string {
	return fmt.Sprintf("projects/%s/subscriptions/%s", tc.projectID, id)
}

func (tc *TestCase) snapshotPath(id string) string {
	return fmt.Sprintf("projects/%s/snapshots/%s", tc.projectID, id)
}

func (tc *TestCase) schemaPath(id string) string {
	return fmt.Sprintf("projects/%s/schemas/%s", tc.projectID, id)
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

func (tc *TestCase) Setup(ctx context.Context, t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)

	var err error
	tc.container, err = gcloud.RunPubsub(ctx,
		"gcr.io/google.com/cloudsdktool/google-cloud-cli:emulators",
		gcloud.WithProjectID(testProject),
		testcontainers.WithLogger(tclog.TestLogger(t)),
		containers.WithTestLogConsumer(t),
	)
	containers.AssertTestContainersError(t, err)
	containers.RegisterContainerCleanup(t, tc.container)

	tc.projectID = tc.container.Settings.ProjectID
	tc.uri = tc.container.URI

	tc.client, err = pubsub.NewClient(ctx, tc.projectID, emulatorOptions(tc.uri)...)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, tc.client.Close()) })
}

func (tc *TestCase) Run(ctx context.Context, t *testing.T) {
	// Pattern 1: admin clients exposed by the high-level pubsub.Client. These are
	// constructed internally by pubsub.NewClient (a variadic-spread call), so this
	// exercises orchestrion's interceptor injection into that path.
	topic, err := tc.client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{
		Name: tc.topicPath(testTopic),
	})
	require.NoError(t, err)

	_, err = tc.client.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name:  tc.subscriptionPath(testSubscription),
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
		Name:         tc.snapshotPath(testSnapshot),
		Subscription: tc.subscriptionPath(testSubscription),
	})
	require.NoError(t, err)

	drain(t, tc.client.SubscriptionAdminClient.ListSnapshots(ctx, &pubsubpb.ListSnapshotsRequest{
		Project: tc.projectPath(),
	}).Next)

	// Pattern 2: a directly-constructed GAPIC SchemaClient (not exposed on
	// pubsub.Client), covering the SchemaClient constructor join-point.
	schemaClient, err := vkit.NewSchemaClient(ctx, emulatorOptions(tc.uri)...)
	require.NoError(t, err)
	defer func() { assert.NoError(t, schemaClient.Close()) }()

	_, err = schemaClient.CreateSchema(ctx, &pubsubpb.CreateSchemaRequest{
		Parent: tc.projectPath(),
		Schema: &pubsubpb.Schema{
			Type:       pubsubpb.Schema_AVRO,
			Definition: avroDefinition,
		},
		SchemaId: testSchema,
	})
	require.NoError(t, err)

	_, err = schemaClient.GetSchema(ctx, &pubsubpb.GetSchemaRequest{Name: tc.schemaPath(testSchema)})
	require.NoError(t, err)

	drain(t, schemaClient.ListSchemas(ctx, &pubsubpb.ListSchemasRequest{Parent: tc.projectPath()}).Next)

	err = schemaClient.DeleteSchema(ctx, &pubsubpb.DeleteSchemaRequest{Name: tc.schemaPath(testSchema)})
	require.NoError(t, err)

	// Pattern 3: a directly-constructed GAPIC TopicAdminClient.
	tac, err := vkit.NewTopicAdminClient(ctx, emulatorOptions(tc.uri)...)
	require.NoError(t, err)
	defer func() { assert.NoError(t, tac.Close()) }()

	_, err = tac.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: tc.topicPath(testTopic)})
	require.NoError(t, err)

	_, err = tac.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: tc.topicPath("missing")})
	require.Error(t, err)
	tc.missingErrMsg = err.Error()

	err = tc.client.SubscriptionAdminClient.DeleteSnapshot(ctx, &pubsubpb.DeleteSnapshotRequest{
		Snapshot: tc.snapshotPath(testSnapshot),
	})
	require.NoError(t, err)

	err = tc.client.SubscriptionAdminClient.DeleteSubscription(ctx, &pubsubpb.DeleteSubscriptionRequest{
		Subscription: tc.subscriptionPath(testSubscription),
	})
	require.NoError(t, err)

	err = tc.client.TopicAdminClient.DeleteTopic(ctx, &pubsubpb.DeleteTopicRequest{
		Topic: tc.topicPath(testTopic),
	})
	require.NoError(t, err)
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		tc.adminTrace("CreateTopic", tc.topicPath(testTopic)),
		tc.adminTrace("CreateSubscription", tc.subscriptionPath(testSubscription)),
		tc.adminTrace("ListTopics", tc.projectPath()),
		tc.adminTrace("ListSubscriptions", tc.projectPath()),
		tc.adminTrace("CreateSnapshot", tc.snapshotPath(testSnapshot)),
		tc.adminTrace("ListSnapshots", tc.projectPath()),
		tc.adminTrace("CreateSchema", tc.projectPath()),
		tc.adminTrace("GetSchema", tc.schemaPath(testSchema)),
		tc.adminTrace("ListSchemas", tc.projectPath()),
		tc.adminTrace("DeleteSchema", tc.schemaPath(testSchema)),
		tc.adminTrace("GetTopic", tc.topicPath(testTopic)),
		tc.adminErrorTrace("GetTopic", tc.topicPath("missing"), tc.missingErrMsg),
		tc.adminTrace("DeleteSnapshot", tc.snapshotPath(testSnapshot)),
		tc.adminTrace("DeleteSubscription", tc.subscriptionPath(testSubscription)),
		tc.adminTrace("DeleteTopic", tc.topicPath(testTopic)),
	}
}

func (tc *TestCase) adminTrace(method, resourcePath string) *trace.Trace {
	return &trace.Trace{
		Tags: map[string]any{
			"name":     "gcp.pubsub.request",
			"type":     "worker",
			"resource": method + " " + resourcePath,
			"service":  "gcp_pubsub_admin.v2.test",
		},
		Meta: map[string]string{
			"span.kind":         "client",
			"component":         "cloud.google.com/go/pubsub.v2",
			"messaging.system":  "googlepubsub",
			"pubsub.method":     method,
			"gcloud.project_id": testProject,
		},
	}
}

func (tc *TestCase) adminErrorTrace(method, resourcePath, errMsg string) *trace.Trace {
	tr := tc.adminTrace(method, resourcePath)
	tr.Meta["error.message"] = errMsg
	return tr
}
