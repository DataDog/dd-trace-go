// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package goka

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lovoo/goka"
	"github.com/lovoo/goka/codec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// kafkaBrokers is the broker list used by the integration test. Override with the
// KAFKA_BROKERS environment variable (comma-separated) if needed.
var kafkaBrokers = []string{"localhost:9092"}

// TestProcessorIntegration exercises the full path against a real Kafka broker:
// an upstream producer injects trace + DSM context via EmitHeaders, and a traced
// goka processor consumes it, continuing the same trace. It is skipped unless the
// INTEGRATION environment variable is set.
func TestProcessorIntegration(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("INTEGRATION environment variable not set")
	}
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")

	mt := mocktracer.Start()
	defer mt.Stop()

	const (
		inputTopic = goka.Stream("dd-goka-integration-in")
		group      = goka.Group("dd-goka-integration-grp")
	)

	tr := NewTracer(WithService("goka-integration"), WithDataStreams())

	// Ensure the input topic exists.
	tm, err := goka.NewTopicManager(kafkaBrokers, goka.DefaultConfig(), goka.NewTopicManagerConfig())
	require.NoError(t, err)
	defer tm.Close()
	require.NoError(t, tm.EnsureStreamExists(string(inputTopic), 1))

	// The callback records the trace ID it observes so we can assert the consume
	// span continued the upstream trace.
	processed := make(chan struct{})
	var gotTraceID string
	cb := tr.WrapCallback(func(ctx goka.Context, _ any) {
		if span, ok := tracer.SpanFromContext(ctx.Context()); ok {
			gotTraceID = span.Context().TraceID()
		}
		close(processed)
	})

	g := goka.DefineGroup(group, goka.Input(inputTopic, new(codec.String), cb))
	p, err := goka.NewProcessor(kafkaBrokers, g, goka.WithContextWrapper(tr.WrapContext))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	// Wait until the processor has joined the group and is consuming, so it sees
	// the message we are about to emit (goka defaults to OffsetNewest).
	require.NoError(t, p.WaitForReady())

	// Emit a message carrying an upstream parent span and a DSM outbound checkpoint.
	parent := tracer.StartSpan("upstream")
	headers := tr.EmitHeaders(tracer.ContextWithSpan(context.Background(), parent), string(inputTopic))
	require.NotEmpty(t, headers)

	em, err := goka.NewEmitter(kafkaBrokers, inputTopic, new(codec.String))
	require.NoError(t, err)
	require.NoError(t, em.EmitSyncWithHeaders("key", "value", headers))
	require.NoError(t, em.Finish())
	parent.Finish()

	select {
	case <-processed:
	case <-time.After(30 * time.Second):
		cancel()
		<-done
		t.Fatal("timed out waiting for the message to be processed")
	}

	cancel()
	require.NoError(t, <-done)

	assert.Equal(t, parent.Context().TraceID(), gotTraceID,
		"consume span should continue the upstream trace")

	var consume *mocktracer.Span
	for _, s := range mt.FinishedSpans() {
		if s.OperationName() == "kafka.consume" {
			consume = s
			break
		}
	}
	require.NotNil(t, consume, "expected a kafka.consume span")
	assert.Equal(t, parent.Context().TraceID(), consume.Context().TraceID())
	assert.Equal(t, parent.Context().SpanID(), consume.ParentID())
	assert.Equal(t, "goka-integration", consume.Tag(ext.ServiceName))
	assert.Equal(t, "Consume Topic "+string(inputTopic), consume.Tag(ext.ResourceName))
	assert.Equal(t, componentName, consume.Tag(ext.Component))
	assert.Equal(t, "consumer", consume.Tag(ext.SpanKind))
	assert.Equal(t, string(inputTopic), consume.Tag(ext.MessagingDestinationName))
}
