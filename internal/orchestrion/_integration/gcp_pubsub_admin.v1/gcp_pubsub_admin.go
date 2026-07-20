// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023-present Datadog, Inc.

//go:build linux || !githubci

// Package gcppubsubadmin exercises orchestrion tracing of Pub/Sub v1 admin
// operations via high-level and GAPIC clients dialing the emulator themselves.
package gcppubsubadmin

import (
	"context"
	"fmt"
	"testing"

	"cloud.google.com/go/pubsub"
	vkit "cloud.google.com/go/pubsub/apiv1"
	"cloud.google.com/go/pubsub/apiv1/pubsubpb"
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
	// Pattern 1: the high-level pubsub.Client. CreateTopic / CreateSubscription are
	// backed by the GAPIC PublisherClient / SubscriberClient constructed internally
	// by pubsub.NewClient (a variadic-spread call), exercising orchestrion's
	// interceptor injection into that path.
	topic, err := tc.client.CreateTopic(ctx, testTopic)
	require.NoError(t, err)

	_, err = tc.client.CreateSubscription(ctx, testSubscription, pubsub.SubscriptionConfig{
		Topic: topic,
	})
	require.NoError(t, err)

	// Pattern 2: directly-constructed GAPIC admin clients.
	pc, err := vkit.NewPublisherClient(ctx, emulatorOptions(tc.uri)...)
	require.NoError(t, err)
	defer func() { assert.NoError(t, pc.Close()) }()

	sc, err := vkit.NewSubscriberClient(ctx, emulatorOptions(tc.uri)...)
	require.NoError(t, err)
	defer func() { assert.NoError(t, sc.Close()) }()

	drain(t, pc.ListTopics(ctx, &pubsubpb.ListTopicsRequest{Project: tc.projectPath()}).Next)
	drain(t, sc.ListSubscriptions(ctx, &pubsubpb.ListSubscriptionsRequest{Project: tc.projectPath()}).Next)

	_, err = sc.CreateSnapshot(ctx, &pubsubpb.CreateSnapshotRequest{
		Name:         tc.snapshotPath(testSnapshot),
		Subscription: tc.subscriptionPath(testSubscription),
	})
	require.NoError(t, err)

	drain(t, sc.ListSnapshots(ctx, &pubsubpb.ListSnapshotsRequest{Project: tc.projectPath()}).Next)

	// SchemaClient is only constructed explicitly (not via pubsub.NewClient).
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

	_, err = pc.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: tc.topicPath(testTopic)})
	require.NoError(t, err)

	_, err = pc.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: tc.topicPath("missing")})
	require.Error(t, err)
	tc.missingErrMsg = err.Error()

	err = sc.DeleteSnapshot(ctx, &pubsubpb.DeleteSnapshotRequest{Snapshot: tc.snapshotPath(testSnapshot)})
	require.NoError(t, err)

	err = sc.DeleteSubscription(ctx, &pubsubpb.DeleteSubscriptionRequest{Subscription: tc.subscriptionPath(testSubscription)})
	require.NoError(t, err)

	err = pc.DeleteTopic(ctx, &pubsubpb.DeleteTopicRequest{Topic: tc.topicPath(testTopic)})
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
			"service":  "gcp_pubsub_admin.v1.test",
		},
		Meta: map[string]string{
			"span.kind":         "client",
			"component":         "cloud.google.com/go/pubsub.v1",
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
