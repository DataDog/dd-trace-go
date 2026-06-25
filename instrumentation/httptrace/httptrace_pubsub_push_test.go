// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

const (
	testProjectID        = "my-project"
	testSubscriptionID   = "my-subscription"
	testSubscriptionName = "projects/my-project/subscriptions/my-subscription"
	testMessageID        = "msg-abc-123"
)

func pubsubPushHeaders() map[string]string {
	return map[string]string{
		"X-Goog-Pubsub-Subscription-Name": testSubscriptionName,
		"X-Goog-Pubsub-Message-Id":        testMessageID,
	}
}

func TestInferredPubsubPushSpans(t *testing.T) {
	t.Setenv("DD_SERVICE", "pubsub-push-server")
	t.Setenv("DD_TRACE_INFERRED_PROXY_SERVICES_ENABLED", "true")
	ResetCfg()

	srvURL := "https://my-service.example.com/push-endpoint"

	t.Run("should create inferred pubsub.receive parent span and http.request child span", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srvURL, nil)
		require.NoError(t, err)

		for k, v := range pubsubPushHeaders() {
			req.Header.Set(k, v)
		}

		_, _, finishSpans := StartRequestSpan(req)
		finishSpans(200, nil)

		spans := mt.FinishedSpans()
		require.Equal(t, 2, len(spans))

		httpSpan := spans[0]
		pubsubSpan := spans[1]

		assert.Equal(t, "pubsub.receive", pubsubSpan.OperationName())
		assert.Equal(t, "http.request", httpSpan.OperationName())
		assert.True(t, httpSpan.ParentID() == pubsubSpan.SpanID())
	})

	t.Run("should set expected tags on the inferred pubsub.receive span", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srvURL, nil)
		require.NoError(t, err)

		for k, v := range pubsubPushHeaders() {
			req.Header.Set(k, v)
		}

		_, _, finishSpans := StartRequestSpan(req)
		finishSpans(200, nil)

		spans := mt.FinishedSpans()
		require.Equal(t, 2, len(spans))

		pubsubSpan := spans[1]

		assert.Equal(t, ext.SpanTypeMessageConsumer, pubsubSpan.Tag(ext.SpanType))
		assert.Equal(t, "pubsub.receive", pubsubSpan.Tag(ext.SpanName))
		assert.Equal(t, "net/http", pubsubSpan.Tag(ext.Component))
		assert.Equal(t, testSubscriptionName, pubsubSpan.Tag(ext.ResourceName))
		assert.Equal(t, ext.SpanKindConsumer, pubsubSpan.Tag(ext.SpanKind))
		assert.Equal(t, testSubscriptionID, pubsubSpan.Tag(ext.MessagingDestinationName))
		assert.Equal(t, "receive", pubsubSpan.Tag(ext.MessagingOperationName))
		assert.Equal(t, testMessageID, pubsubSpan.Tag(ext.MessagingMessageID))
		assert.Equal(t, testMessageID, pubsubSpan.Tag("message_id"))
		assert.Equal(t, testProjectID, pubsubSpan.Tag("gcloud.project_id"))
		assert.Equal(t, "googlepubsub", pubsubSpan.Tag(ext.MessagingSystem))
		assert.Equal(t, float64(1), pubsubSpan.Tag("_dd.inferred_span"))
	})

	t.Run("should propagate error status to both spans", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", fmt.Sprintf("%s/error", srvURL), nil)
		require.NoError(t, err)

		for k, v := range pubsubPushHeaders() {
			req.Header.Set(k, v)
		}

		_, _, finishSpans := StartRequestSpan(req)
		finishSpans(500, nil)

		spans := mt.FinishedSpans()
		require.Equal(t, 2, len(spans))

		httpSpan := spans[0]
		pubsubSpan := spans[1]

		assert.Equal(t, "pubsub.receive", pubsubSpan.OperationName())
		assert.Equal(t, "http.request", httpSpan.OperationName())
		assert.True(t, httpSpan.ParentID() == pubsubSpan.SpanID())
		assert.Equal(t, httpSpan.Tag(ext.HTTPCode), pubsubSpan.Tag(ext.HTTPCode))
		assert.Equal(t, "500: Internal Server Error", pubsubSpan.Tag(ext.ErrorMsg))
		assert.Equal(t, "500: Internal Server Error", httpSpan.Tag(ext.ErrorMsg))
	})

	t.Run("should not create inferred span when push headers are absent", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srvURL, nil)
		require.NoError(t, err)

		_, _, finishSpans := StartRequestSpan(req)
		finishSpans(200, nil)

		spans := mt.FinishedSpans()
		require.Equal(t, 1, len(spans))
		assert.Equal(t, "http.request", spans[0].OperationName())
	})

	t.Run("should not create inferred span when subscription name header is missing", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srvURL, nil)
		require.NoError(t, err)

		req.Header.Set("X-Goog-Pubsub-Message-Id", testMessageID)

		_, _, finishSpans := StartRequestSpan(req)
		finishSpans(200, nil)

		spans := mt.FinishedSpans()
		require.Equal(t, 1, len(spans))
		assert.Equal(t, "http.request", spans[0].OperationName())
	})

	t.Run("should not create inferred span when message ID header is missing", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srvURL, nil)
		require.NoError(t, err)

		req.Header.Set("X-Goog-Pubsub-Subscription-Name", testSubscriptionName)

		_, _, finishSpans := StartRequestSpan(req)
		finishSpans(200, nil)

		spans := mt.FinishedSpans()
		require.Equal(t, 1, len(spans))
		assert.Equal(t, "http.request", spans[0].OperationName())
	})

	t.Run("should not create more than one pubsub inferred span for a local trace", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srvURL, nil)
		require.NoError(t, err)

		for k, v := range pubsubPushHeaders() {
			req.Header.Set(k, v)
		}

		_, ctx, finishSpans1 := StartRequestSpan(req)
		finishSpans1(200, nil)

		req2 := req.WithContext(ctx)
		_, _, finishSpans2 := StartRequestSpan(req2)
		finishSpans2(200, nil)

		spans := mt.FinishedSpans()
		require.Equal(t, 3, len(spans))

		httpSpan := spans[0]
		pubsubSpan := spans[1]

		assert.Equal(t, "pubsub.receive", pubsubSpan.OperationName())
		assert.Equal(t, "http.request", httpSpan.OperationName())
		assert.Equal(t, float64(1), pubsubSpan.Tag("_dd.inferred_span"))
		assert.True(t, httpSpan.ParentID() == pubsubSpan.SpanID())
	})
}

func TestInferredPubsubPushSpansWithPropagationAsSpanLinks(t *testing.T) {
	t.Setenv("DD_SERVICE", "pubsub-push-server")
	t.Setenv("DD_TRACE_INFERRED_PROXY_SERVICES_ENABLED", "true")
	t.Setenv("DD_GOOGLE_CLOUD_PUBSUB_PROPAGATION_AS_SPAN_LINKS", "true")
	ResetCfg()
	defer ResetCfg()

	srvURL := "https://my-service.example.com/push-endpoint"

	t.Run("inferred span should carry a span link to the producer, not reparent", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		// Simulate a producer that injected its trace context into the push request.
		producerSpan, _ := tracer.StartSpanFromContext(t.Context(), "producer")
		req, err := http.NewRequest("POST", srvURL, nil)
		require.NoError(t, err)
		for k, v := range pubsubPushHeaders() {
			req.Header.Set(k, v)
		}
		require.NoError(t, tracer.Inject(producerSpan.Context(), tracer.HTTPHeadersCarrier(req.Header)))
		producerSpan.Finish()

		_, _, finishSpans := StartRequestSpan(req)
		finishSpans(200, nil)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 3) // producer, http.request, pubsub.receive

		// Locate spans by operation name
		var producerFinished, httpSpan, pubsubSpan *mocktracer.Span
		for _, s := range spans {
			switch s.OperationName() {
			case "producer":
				producerFinished = s
			case "http.request":
				httpSpan = s
			case "pubsub.receive":
				pubsubSpan = s
			}
		}
		require.NotNil(t, producerFinished)
		require.NotNil(t, httpSpan)
		require.NotNil(t, pubsubSpan)

		// pubsub.receive must NOT inherit the producer trace.
		assert.NotEqual(t, producerFinished.TraceID(), pubsubSpan.TraceID(), "pubsub.receive should start a new trace")
		assert.Equal(t, uint64(0), pubsubSpan.ParentID(), "pubsub.receive should have no parent")

		// The producer span must be recorded as a span link on pubsub.receive.
		links := pubsubSpan.Links()
		require.Len(t, links, 1, "expected exactly one span link on pubsub.receive")
		assert.Equal(t, producerFinished.SpanID(), links[0].SpanID)

		// http.request must still be a child of pubsub.receive (not of the producer).
		assert.Equal(t, pubsubSpan.SpanID(), httpSpan.ParentID())
	})

	t.Run("inferred span has no span link when no producer trace context is present", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srvURL, nil)
		require.NoError(t, err)
		for k, v := range pubsubPushHeaders() {
			req.Header.Set(k, v)
		}

		_, _, finishSpans := StartRequestSpan(req)
		finishSpans(200, nil)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 2)

		var pubsubSpan *mocktracer.Span
		for _, s := range spans {
			if s.OperationName() == "pubsub.receive" {
				pubsubSpan = s
			}
		}
		require.NotNil(t, pubsubSpan)
		assert.Nil(t, pubsubSpan.Links(), "no span links expected when there is no producer trace context")
	})
}

func TestExtractInferredPubsubContext(t *testing.T) {
	t.Run("returns context when both headers are present", func(t *testing.T) {
		headers := http.Header{}
		headers.Set(PubsubHeaderSubscriptionName, testSubscriptionName)
		headers.Set(PubsubHeaderMessageID, testMessageID)

		ctx := extractInferredPubsubContext(headers)

		assert.Equal(t, testSubscriptionName, ctx.subscriptionName)
		assert.Equal(t, testProjectID, ctx.projectID)
		assert.Equal(t, testSubscriptionID, ctx.subscriptionID)
		assert.Equal(t, testMessageID, ctx.messageID)
	})

	t.Run("returns nil when subscription name header is absent", func(t *testing.T) {
		headers := http.Header{}
		headers.Set(PubsubHeaderMessageID, testMessageID)

		ctx := extractInferredPubsubContext(headers)

		require.Nil(t, ctx)
	})

	t.Run("returns nil when message ID header is absent", func(t *testing.T) {
		headers := http.Header{}
		headers.Set(PubsubHeaderSubscriptionName, testSubscriptionName)

		ctx := extractInferredPubsubContext(headers)

		require.Nil(t, ctx)
	})

	t.Run("returns nil when both headers are absent", func(t *testing.T) {
		headers := http.Header{}

		ctx := extractInferredPubsubContext(headers)

		require.Nil(t, ctx)
	})

	t.Run("returns nil when subscription name has wrong format", func(t *testing.T) {
		for _, bad := range []string{
			"",
			"my-subscription",
			"projects/my-project",
			"projects/my-project/topics/my-topic",
			"projects/my-project/subscriptions",
			"orgs/my-project/subscriptions/my-subscription",
			"projects//subscriptions/my-subscription",
			"projects/my-project/subscriptions/my-subscription/extra",
		} {
			t.Run(fmt.Sprintf("%q", bad), func(t *testing.T) {
				headers := http.Header{}
				headers.Set(PubsubHeaderSubscriptionName, bad)
				headers.Set(PubsubHeaderMessageID, testMessageID)

				ctx := extractInferredPubsubContext(headers)

				require.Nil(t, ctx)
			})
		}
	})
}
