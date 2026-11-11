// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package pubsubtrace

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// resource is a Topic or Subscription that only has a name, like the SDK
// types: the tracer cannot fetch metadata through it.
type resource string

func (r resource) String() string { return string(r) }

func newTestTracer() *Tracer {
	return NewTracer(instrumentation.Load(instrumentation.PackageGCPPubsubV2), instrumentation.PackageGCPPubsubV2)
}

func TestPublishTopicName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		topic   string
		want    any
		project any
	}{
		{name: "full path", topic: "projects/commerce/topics/orders", want: "projects/commerce/topics/orders", project: "commerce"},
		// Projects created under an organization domain have a domain-scoped ID.
		{name: "domain-scoped project", topic: "projects/example.com:commerce/topics/orders", want: "projects/example.com:commerce/topics/orders", project: "example.com:commerce"},
		{name: "short name", topic: "orders"},
		{name: "deleted topic placeholder", topic: "_deleted-topic_"},
		{name: "subscription path", topic: "projects/commerce/subscriptions/orders", project: "commerce"},
		{name: "missing project", topic: "projects//topics/orders"},
		{name: "missing topic", topic: "projects/commerce/topics/", project: "commerce"},
		{name: "extra segment", topic: "projects/commerce/topics/orders/x", project: "commerce"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			_, closeSpan := newTestTracer().TracePublish(context.Background(), resource(tc.topic), &Message{Data: []byte("hello")})
			closeSpan("server-id", nil)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			s := spans[0]
			assert.Equal(t, tc.want, s.Tag(ext.PubsubTopicName))
			assert.Nil(t, s.Tag(ext.PubsubSubscriptionName))
			// The existing tags are unchanged.
			assert.Equal(t, tc.topic, s.Tag(ext.ResourceName))
			assert.Equal(t, tc.topic, s.Tag(ext.MessagingDestinationName))
			assert.Equal(t, tc.project, s.Tag(ext.GCPProjectID))
		})
	}
}

func TestReceiveSubscriptionName(t *testing.T) {
	for _, tc := range []struct {
		name         string
		subscription string
		want         any
		project      any
	}{
		{name: "full path", subscription: "projects/analytics/subscriptions/warehouse-orders", want: "projects/analytics/subscriptions/warehouse-orders", project: "analytics"},
		{name: "domain-scoped project", subscription: "projects/example.com:analytics/subscriptions/warehouse-orders", want: "projects/example.com:analytics/subscriptions/warehouse-orders", project: "example.com:analytics"},
		{name: "short name", subscription: "warehouse-orders"},
		{name: "topic path", subscription: "projects/analytics/topics/orders", project: "analytics"},
		{name: "missing subscription", subscription: "projects/analytics/subscriptions/", project: "analytics"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			receive := newTestTracer().TraceReceiveFunc(resource(tc.subscription))
			_, finish := receive(context.Background(), &Message{ID: "id", Data: []byte("hello")})
			finish()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			s := spans[0]
			assert.Equal(t, tc.want, s.Tag(ext.PubsubSubscriptionName))
			// The topic of a subscription is not known to the consumer.
			assert.Nil(t, s.Tag(ext.PubsubTopicName))
			// The existing tags are unchanged.
			assert.Equal(t, tc.subscription, s.Tag(ext.ResourceName))
			assert.Equal(t, tc.subscription, s.Tag(ext.MessagingDestinationName))
			assert.Equal(t, tc.project, s.Tag(ext.GCPProjectID))
		})
	}
}

// TestTopicAndSubscriptionNames publishes to a topic and receives from a
// differently named subscription, in the same project and across projects.
// Each identity keeps the project of its own path.
func TestTopicAndSubscriptionNames(t *testing.T) {
	for _, tc := range []struct {
		name         string
		topic        string
		subscription string
	}{
		{name: "same project", topic: "projects/commerce/topics/orders", subscription: "projects/commerce/subscriptions/warehouse-orders"},
		{name: "across projects", topic: "projects/commerce/topics/orders", subscription: "projects/analytics/subscriptions/warehouse-orders"},
		{name: "across domain-scoped projects", topic: "projects/example.com:commerce/topics/orders", subscription: "projects/example.com:analytics/subscriptions/warehouse-orders"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			tr := newTestTracer()

			msg := &Message{Data: []byte("hello")}
			ctx, closeSpan := tr.TracePublish(context.Background(), resource(tc.topic), msg)
			closeSpan("server-id", nil)
			_, finish := tr.TraceReceiveFunc(resource(tc.subscription))(ctx, msg)
			finish()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 2)
			publish, receive := spans[0], spans[1]
			assert.Equal(t, tc.topic, publish.Tag(ext.PubsubTopicName))
			assert.Nil(t, publish.Tag(ext.PubsubSubscriptionName))
			assert.Equal(t, tc.subscription, receive.Tag(ext.PubsubSubscriptionName))
			assert.Nil(t, receive.Tag(ext.PubsubTopicName), "the topic must not be derived for the consumer")
			assert.Equal(t, projectIDFromResourceName(tc.topic), publish.Tag(ext.GCPProjectID))
			assert.Equal(t, projectIDFromResourceName(tc.subscription), receive.Tag(ext.GCPProjectID))
		})
	}
}
