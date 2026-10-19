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
	publisher, err := rueidis.NewClient(opt)
	require.NoError(t, err)
	t.Cleanup(publisher.Close)

	for _, traced := range []bool{false, true} {
		name := "plain"
		if traced {
			name = "traced"
		}
		t.Run(name, func(t *testing.T) {
			for _, factory := range dedicatedClientFactories {
				t.Run(factory.name, func(t *testing.T) {
					for _, method := range []string{"Do", "DoMulti", "Receive"} {
						t.Run(method, func(t *testing.T) {
							mt := mocktracer.Start()
							defer mt.Stop()

							var c rueidis.Client
							var err error
							if traced {
								c, err = NewClient(opt, WithRawCommand(true), WithService("dedicated-redis"))
							} else {
								c, err = rueidis.NewClient(opt)
							}
							require.NoError(t, err)
							defer c.Close()

							root, ctx := tracer.StartSpanFromContext(context.Background(), "test.root")
							defer root.Finish()
							ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
							defer cancel()

							factory.run(t, c, func(dc rueidis.DedicatedClient) {
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
								var receiveErr chan error
								switch method {
								case "Do":
									require.NoError(t, dc.Do(ctx, subscribe).Error())
								case "DoMulti":
									results := dc.DoMulti(ctx, subscribe)
									require.Len(t, results, 1)
									require.NoError(t, results[0].Error())
								case "Receive":
									receiveErr = make(chan error, 1)
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
							})

							spans := mt.FinishedSpans()
							if !traced {
								require.Empty(t, spans)
								return
							}
							require.Len(t, spans, 1)
							span := spans[0]
							assert.Equal(t, root.Context().SpanID(), span.ParentID())
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
						})
					}
				})
			}
		})
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
