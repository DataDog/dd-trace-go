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
)

type TestCase struct {
	container *gcloud.GCloudContainer
	client    *pubsub.Client
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

func (tc *TestCase) topicPath(id string) string {
	return fmt.Sprintf("projects/%s/topics/%s", tc.projectID, id)
}

func (tc *TestCase) subscriptionPath(id string) string {
	return fmt.Sprintf("projects/%s/subscriptions/%s", tc.projectID, id)
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

	// Pattern 2: a directly-constructed GAPIC admin client.
	tac, err := vkit.NewTopicAdminClient(ctx, emulatorOptions(tc.uri)...)
	require.NoError(t, err)
	defer func() { assert.NoError(t, tac.Close()) }()

	_, err = tac.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: tc.topicPath(testTopic)})
	require.NoError(t, err)
}

func (tc *TestCase) ExpectedTraces() trace.Traces {
	return trace.Traces{
		tc.adminTrace("CreateTopic", tc.topicPath(testTopic)),
		tc.adminTrace("CreateSubscription", tc.subscriptionPath(testSubscription)),
		tc.adminTrace("GetTopic", tc.topicPath(testTopic)),
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
