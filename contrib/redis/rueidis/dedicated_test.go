// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || !githubci

package rueidis

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/instrumentation/testutils/containers/v2"
	"github.com/redis/rueidis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

var dedicatedClientFactories = []struct {
	name string
	run  func(*testing.T, rueidis.Client, func(rueidis.DedicatedClient))
}{
	{
		name: "Dedicate",
		run: func(t *testing.T, c rueidis.Client, fn func(rueidis.DedicatedClient)) {
			dc, cancel := c.Dedicate()
			defer cancel()
			fn(dc)
		},
	},
	{
		name: "Dedicated",
		run: func(t *testing.T, c rueidis.Client, fn func(rueidis.DedicatedClient)) {
			require.NoError(t, c.Dedicated(func(dc rueidis.DedicatedClient) error {
				fn(dc)
				return nil
			}))
		},
	},
}

func TestDedicatedClientPubSub(t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)
	_, addr := containers.StartRedisTestContainer(t)
	opt := rueidis.ClientOption{InitAddress: []string{addr}, DisableCache: true}
	publisher := newDedicatedPubSubClient(t, opt, false)
	t.Cleanup(publisher.Close)

	for _, client := range []struct {
		name   string
		traced bool
	}{
		{name: "plain"},
		{name: "traced", traced: true},
	} {
		for _, factory := range dedicatedClientFactories {
			for _, method := range []string{"Do", "DoMulti", "Receive"} {
				t.Run(client.name+"/"+factory.name+"/"+method, func(t *testing.T) {
					mt := mocktracer.Start()
					defer mt.Stop()
					c := newDedicatedPubSubClient(t, opt, client.traced)
					defer c.Close()

					root, ctx := tracer.StartSpanFromContext(context.Background(), "test.root")
					defer root.Finish()
					factory.run(t, c, func(dc rueidis.DedicatedClient) {
						testDedicatedPubSubDelivery(t, ctx, dc, publisher, method)
					})

					spans := mt.FinishedSpans()
					if !client.traced {
						require.Empty(t, spans)
						return
					}
					assertDedicatedPubSubSpans(t, spans, root.Context().SpanID(), method)
				})
			}
		}
	}
}

func newDedicatedPubSubClient(t *testing.T, opt rueidis.ClientOption, traced bool) rueidis.Client {
	t.Helper()
	var c rueidis.Client
	var err error
	if traced {
		c, err = NewClient(opt, WithRawCommand(true), WithService("dedicated-redis"))
	} else {
		c, err = rueidis.NewClient(opt)
	}
	require.NoError(t, err)
	return c
}

func testDedicatedPubSubDelivery(t *testing.T, ctx context.Context, dc rueidis.DedicatedClient, publisher rueidis.Client, method string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	channel := t.Name()
	got := make(chan string, 1)
	subscribed := make(chan struct{}, 1)
	onMessage := func(msg rueidis.PubSubMessage) { got <- msg.Message }
	hooks := rueidis.PubSubHooks{
		OnSubscription: func(s rueidis.PubSubSubscription) {
			if s.Kind == "subscribe" {
				subscribed <- struct{}{}
			}
		},
	}
	if method != "Receive" {
		hooks.OnMessage = onMessage
	}
	dc.SetPubSubHooks(hooks)
	subscribe := dc.B().Subscribe().Channel(channel).Build()
	switch method {
	case "Do":
		require.NoError(t, dc.Do(ctx, subscribe).Error())
	case "DoMulti":
		results := dc.DoMulti(ctx, subscribe)
		require.Len(t, results, 1)
		require.NoError(t, results[0].Error())
	case "Receive":
		receiveErr := make(chan error, 1)
		go func() { receiveErr <- dc.Receive(ctx, subscribe, onMessage) }()
		defer func() {
			cancel()
			require.ErrorIs(t, <-receiveErr, ctx.Err())
		}()
		select {
		case <-subscribed:
		case <-ctx.Done():
			t.Fatal("subscription did not reach the dedicated connection")
		}
	}

	n, err := publisher.Do(ctx, publisher.B().Publish().Channel(channel).Message("hello").Build()).AsInt64()
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	select {
	case msg := <-got:
		require.Equal(t, "hello", msg)
	case <-ctx.Done():
		t.Fatal("PUBLISH reported a receiver, but the dedicated hook received no message")
	}
}

func assertDedicatedPubSubSpans(t *testing.T, spans []*mocktracer.Span, parentID uint64, method string) {
	t.Helper()
	require.Len(t, spans, 1)
	span := spans[0]
	assert.Equal(t, parentID, span.ParentID())
	assert.Equal(t, "redis.command", span.OperationName())
	assert.Equal(t, "SUBSCRIBE", span.Tag(ext.ResourceName))
	assert.Equal(t, "SUBSCRIBE "+t.Name(), span.Tag(ext.RedisRawCommand))
	assert.Equal(t, "dedicated-redis", span.Tag(ext.ServiceName))
	assert.Equal(t, "redis/rueidis", span.Tag(ext.Component))
	assert.Equal(t, "client", span.Tag(ext.SpanKind))
	if method == "Receive" {
		assert.Equal(t, context.Canceled.Error(), span.Tag(ext.ErrorMsg))
	} else {
		assert.Nil(t, span.Tag(ext.Error))
	}
	if method == "Do" {
		assert.Equal(t, "false", span.Tag(ext.RedisClientCacheHit))
	} else {
		assert.Nil(t, span.Tag(ext.RedisClientCacheHit))
	}
}

func TestDedicatedClientClose(t *testing.T) {
	containers.SkipIfProviderIsNotHealthy(t)
	_, addr := containers.StartRedisTestContainer(t)
	for _, factory := range dedicatedClientFactories {
		t.Run(factory.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			c, err := NewClient(rueidis.ClientOption{InitAddress: []string{addr}, DisableCache: true})
			require.NoError(t, err)
			defer c.Close()
			factory.run(t, c, func(dc rueidis.DedicatedClient) { dc.Close() })

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			pong, err := c.Do(ctx, c.B().Ping().Build()).ToString()
			require.NoError(t, err, "closing a dedicated connection must not close the shared client")
			assert.Equal(t, "PONG", pong)
		})
	}
}
