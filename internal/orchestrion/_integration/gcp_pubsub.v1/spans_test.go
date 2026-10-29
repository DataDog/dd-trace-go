// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package gcppubsub

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"cloud.google.com/go/pubsub"
	vkit "cloud.google.com/go/pubsub/apiv1"
	"cloud.google.com/go/pubsub/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/pstest"
	"example.com/pubsubdep/v1admin"
	pubsubcontrib "github.com/DataDog/dd-trace-go/contrib/cloud.google.com/go/pubsub.v1/v2"
	"github.com/DataDog/orchestrion/runtime/built"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/internal/otelc"
)

const spanComponent = "cloud.google.com/go/pubsub.v1"

// The tests below count spans exactly, which the harness suites cannot do
// because they match the expected spans as a subset. They use the in-process
// pstest server, so they need no container.

// TestPublishReceiveSpans checks that each published message gets one publish
// span, finished by Get, and each received message one receive span, child of
// its publish span.
func TestPublishReceiveSpans(t *testing.T) {
	ctx := context.Background()
	srv := newServer(t)
	spans := startTracer(t)

	client := newClient(ctx, t, srv)
	topic, err := client.CreateTopic(ctx, "spans-topic")
	require.NoError(t, err)
	sub, err := client.CreateSubscription(ctx, "spans-subscription", pubsub.SubscriptionConfig{Topic: topic})
	require.NoError(t, err)

	const messages = 3
	for i := range messages {
		_, err := topic.Publish(ctx, &pubsub.Message{Data: fmt.Appendf(nil, "message %d", i)}).Get(ctx)
		require.NoError(t, err)
	}
	topic.Stop()
	receive(ctx, t, sub, messages)

	got := spans()
	assert.Equal(t, map[string]int{
		"pubsub.publish":                        messages,
		"pubsub.receive":                        messages,
		"gcp.pubsub.request CreateTopic":        1,
		"gcp.pubsub.request CreateSubscription": 1,
	}, countSpans(t, got))

	publishIDs := make(map[uint64]bool)
	for _, s := range got {
		if s.Operation == "pubsub.publish" {
			publishIDs[s.SpanID] = true
		}
	}
	for _, s := range got {
		if s.Operation == "pubsub.receive" {
			assert.Truef(t, publishIDs[s.ParentID], "receive span %d must be a child of a publish span", s.SpanID)
		}
	}
}

// TestContribPublishSpans checks that a message published through the contrib
// gets one publish span, not one from the contrib and one from the
// instrumented Publish.
func TestContribPublishSpans(t *testing.T) {
	if built.WithOrchestrion {
		t.Skip("orchestrion reports two publish spans here, one from the contrib and one from Topic.Publish")
	}
	ctx := context.Background()
	srv := newServer(t)
	spans := startTracer(t)

	client := newClient(ctx, t, srv)
	topic, err := client.CreateTopic(ctx, "contrib-topic")
	require.NoError(t, err)
	_, err = pubsubcontrib.Publish(ctx, topic, &pubsub.Message{Data: []byte("contrib")}).Get(ctx)
	require.NoError(t, err)
	topic.Stop()

	assert.Equal(t, map[string]int{
		"pubsub.publish":                 1,
		"gcp.pubsub.request CreateTopic": 1,
	}, countSpans(t, spans()))
}

// TestAdminSpans checks that each admin client gets the interceptor once,
// whether pubsub.NewClient, pubsub.NewSchemaClient, a test function or a
// dependency module builds it.
func TestAdminSpans(t *testing.T) {
	ctx := context.Background()
	srv := newServer(t)
	spans := startTracer(t)

	client := newClient(ctx, t, srv)
	topic, err := client.CreateTopic(ctx, "admin-topic")
	require.NoError(t, err)
	sub, err := client.CreateSubscription(ctx, "admin-subscription", pubsub.SubscriptionConfig{Topic: topic})
	require.NoError(t, err)

	schemas, err := pubsub.NewSchemaClient(ctx, adminProject, emulatorOptions(srv.Addr)...)
	require.NoError(t, err)
	drain(t, schemas.Schemas(ctx, pubsub.SchemaViewBasic).Next)
	require.NoError(t, schemas.Close())

	pc, err := vkit.NewPublisherClient(ctx, emulatorOptions(srv.Addr)...)
	require.NoError(t, err)
	_, err = pc.GetTopic(ctx, &pubsubpb.GetTopicRequest{Topic: topic.String()})
	require.NoError(t, err)
	require.NoError(t, pc.Close())

	sc, err := vkit.NewSubscriberClient(ctx, emulatorOptions(srv.Addr)...)
	require.NoError(t, err)
	_, err = sc.GetSubscription(ctx, &pubsubpb.GetSubscriptionRequest{Subscription: sub.String()})
	require.NoError(t, err)
	require.NoError(t, sc.Close())

	schc, err := vkit.NewSchemaClient(ctx, emulatorOptions(srv.Addr)...)
	require.NoError(t, err)
	drain(t, schc.ListSchemas(ctx, &pubsubpb.ListSchemasRequest{Parent: "projects/" + adminProject}).Next)
	require.NoError(t, schc.Close())

	require.NoError(t, v1admin.ListTopics(ctx, adminProject, emulatorOptions(srv.Addr)...))

	require.NoError(t, sub.Delete(ctx))
	require.NoError(t, topic.Delete(ctx))

	assert.Equal(t, map[string]int{
		"gcp.pubsub.request CreateTopic":        1,
		"gcp.pubsub.request CreateSubscription": 1,
		"gcp.pubsub.request ListSchemas":        2,
		"gcp.pubsub.request GetTopic":           1,
		"gcp.pubsub.request GetSubscription":    1,
		"gcp.pubsub.request ListTopics":         1,
		"gcp.pubsub.request DeleteSubscription": 1,
		"gcp.pubsub.request DeleteTopic":        1,
	}, countSpans(t, spans()))
}

func newServer(t *testing.T) *pstest.Server {
	srv := pstest.NewServer()
	t.Cleanup(func() { assert.NoError(t, srv.Close()) })
	return srv
}

func newClient(ctx context.Context, t *testing.T, srv *pstest.Server) *pubsub.Client {
	client, err := pubsub.NewClient(ctx, adminProject, emulatorOptions(srv.Addr)...)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	return client
}

// receive acks messages until it has seen n of them.
func receive(ctx context.Context, t *testing.T, sub *pubsub.Subscription, n int) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu   sync.Mutex
		seen int
	)
	require.NoError(t, sub.Receive(ctx, func(_ context.Context, msg *pubsub.Message) {
		msg.Ack()
		mu.Lock()
		defer mu.Unlock()
		if seen++; seen == n {
			cancel()
		}
	}))
	require.Equal(t, n, seen)
}

// startTracer starts the test tracer and returns a function that stops it and
// returns the spans it reported.
func startTracer(t *testing.T) func() []*agenttest.Span {
	require.True(t, built.WithOrchestrion || otelc.Enabled(),
		"this test must be run with either orchestrion or otelc enabled")
	tr, agent, err := tracertest.Bootstrap(t,
		tracer.WithSampler(tracer.NewAllSampler()),
		tracer.WithLogStartup(false),
	)
	require.NoError(t, err)
	lister, ok := agent.(agenttest.SpanLister)
	require.True(t, ok, "the test agent must list its spans")
	return func() []*agenttest.Span {
		tr.Stop()
		return lister.Spans()
	}
}

// countSpans counts the spans of this integration by operation name, and admin
// spans by method too.
func countSpans(t *testing.T, spans []*agenttest.Span) map[string]int {
	counts := make(map[string]int)
	defer func() { t.Logf("%s spans: %v", spanComponent, counts) }()
	for _, s := range spans {
		if s.Meta["component"] != spanComponent {
			continue
		}
		key := s.Operation
		if method := s.Meta["pubsub.method"]; method != "" {
			key += " " + method
		}
		counts[key]++
	}
	return counts
}
