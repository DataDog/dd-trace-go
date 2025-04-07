// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.
package rueidis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

var (
	redisAddrs = []string{fmt.Sprintf("127.0.0.1:%d", 6379)}
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestNewClient(t *testing.T) {
	testutils.SetGlobalServiceName(t, "global-service")

	tests := []struct {
		name            string
		opts            []Option
		runTest         func(*testing.T, context.Context, rueidis.Client)
		assertSpans     func(*testing.T, []*mocktracer.Span)
		wantServiceName string
	}{
		{
			name: "Test SET command with raw command",
			opts: []Option{
				WithRawCommand(true),
				WithService("test-service"),
			},
			runTest: func(t *testing.T, ctx context.Context, client rueidis.Client) {
				assert.NoError(t, client.Do(ctx, client.B().Set().Key("test_key").Value("test_value").Build()).Error())
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1)

				span := spans[0]
				assert.Equal(t, "SET", span.Tag(ext.ResourceName))
				assert.Equal(t, "SET test_key test_value", span.Tag(ext.RedisRawCommand))
				assert.Equal(t, "false", span.Tag(ext.RedisClientCacheHit))
				assert.Less(t, span.Tag(ext.RedisClientCacheTTL), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePXAT), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePTTL), float64(0))
				assert.Nil(t, span.Tag(ext.Error))
			},
			wantServiceName: "test-service",
		},
		{
			name: "Test SET command without raw command",
			opts: nil,
			runTest: func(t *testing.T, ctx context.Context, client rueidis.Client) {
				require.NoError(t, client.Do(ctx, client.B().Set().Key("test_key").Value("test_value").Build()).Error())
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1)

				span := spans[0]
				assert.Equal(t, "SET", span.Tag(ext.ResourceName))
				assert.Nil(t, span.Tag(ext.RedisRawCommand))
				assert.Equal(t, "false", span.Tag(ext.RedisClientCacheHit))
				assert.Less(t, span.Tag(ext.RedisClientCacheTTL), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePXAT), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePTTL), float64(0))
				assert.Nil(t, span.Tag(ext.Error))
			},
			wantServiceName: "global-service",
		},
		{
			name: "Test SET GET multi command",
			opts: []Option{
				WithRawCommand(true),
			},
			runTest: func(t *testing.T, ctx context.Context, client rueidis.Client) {
				resp := client.DoMulti(ctx, client.B().Set().Key("test_key").Value("test_value").Build(), client.B().Get().Key("test_key").Build())
				require.Len(t, resp, 2)
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1)

				span := spans[0]
				assert.Equal(t, "SET GET", span.Tag(ext.ResourceName))
				assert.Equal(t, "SET test_key test_value GET test_key", span.Tag(ext.RedisRawCommand))
				assert.Nil(t, span.Tag(ext.RedisClientCacheHit))
				assert.Nil(t, span.Tag(ext.RedisClientCacheTTL))
				assert.Nil(t, span.Tag(ext.RedisClientCachePXAT))
				assert.Nil(t, span.Tag(ext.RedisClientCachePTTL))
				assert.Nil(t, span.Tag(ext.Error))
			},
			wantServiceName: "global-service",
		},
		{
			name: "Test HMGET command",
			opts: []Option{
				WithRawCommand(true),
			},
			runTest: func(t *testing.T, ctx context.Context, client rueidis.Client) {
				assert.NoError(t, client.DoCache(ctx, client.B().Hmget().Key("mk").Field("1", "2").Cache(), time.Minute).Error())
				resp, err := client.DoCache(ctx, client.B().Hmget().Key("mk").Field("1", "2").Cache(), time.Minute).ToArray()
				require.Len(t, resp, 2)
				require.NoError(t, err)
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 2)

				span := spans[0]
				assert.Equal(t, "HMGET", span.Tag(ext.ResourceName))
				assert.Equal(t, "HMGET mk 1 2", span.Tag(ext.RedisRawCommand))
				assert.Equal(t, "false", span.Tag(ext.RedisClientCacheHit))
				assert.Less(t, span.Tag(ext.RedisClientCacheTTL), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePXAT), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePTTL), float64(0))
				assert.Nil(t, span.Tag(ext.Error))

				span = spans[1]
				assert.Equal(t, "HMGET", span.Tag(ext.ResourceName))
				assert.Equal(t, "HMGET mk 1 2", span.Tag(ext.RedisRawCommand))
				assert.Equal(t, "false", span.Tag(ext.RedisClientCacheHit))
				assert.Less(t, span.Tag(ext.RedisClientCacheTTL), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePXAT), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePTTL), float64(0))
				assert.Nil(t, span.Tag(ext.Error))
			},
			wantServiceName: "global-service",
		},
		{
			name: "Test GET stream command",
			opts: []Option{
				WithRawCommand(true),
			},
			runTest: func(t *testing.T, ctx context.Context, client rueidis.Client) {
				resp := client.DoStream(ctx, client.B().Get().Key("test_key").Build())
				require.NoError(t, resp.Error())
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1)

				span := spans[0]
				assert.Equal(t, "GET", span.Tag(ext.ResourceName))
				assert.Equal(t, "GET test_key", span.Tag(ext.RedisRawCommand))
				assert.Nil(t, span.Tag(ext.RedisClientCacheHit))
				assert.Nil(t, span.Tag(ext.RedisClientCacheTTL))
				assert.Nil(t, span.Tag(ext.RedisClientCachePXAT))
				assert.Nil(t, span.Tag(ext.RedisClientCachePTTL))
				assert.Nil(t, span.Tag(ext.Error))
			},
			wantServiceName: "global-service",
		},
		{
			name: "Test multi command should be limited to 5",
			opts: []Option{
				WithRawCommand(true),
			},
			runTest: func(_ *testing.T, ctx context.Context, client rueidis.Client) {
				ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Nanosecond)
				client.DoMulti(
					ctxWithTimeout,
					client.B().Set().Key("k1").Value("v1").Build(),
					client.B().Get().Key("k1").Build(),
					client.B().Set().Key("k2").Value("v2").Build(),
					client.B().Get().Key("k2").Build(),
					client.B().Set().Key("k3").Value("v3").Build(),
					client.B().Get().Key("k3").Build(),
				)
				cancel()
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1)

				span := spans[0]
				assert.Equal(t, "SET GET SET GET SET", span.Tag(ext.ResourceName))
				assert.Equal(t, "SET k1 v1 GET k1 SET k2 v2 GET k2 SET k3 v3", span.Tag(ext.RedisRawCommand))
				assert.Nil(t, span.Tag(ext.RedisClientCacheHit))
				assert.Nil(t, span.Tag(ext.RedisClientCacheTTL))
				assert.Nil(t, span.Tag(ext.RedisClientCachePXAT))
				assert.Nil(t, span.Tag(ext.RedisClientCachePTTL))
				assert.Equal(t, context.DeadlineExceeded.Error(), span.Tag(ext.ErrorMsg))
			},
			wantServiceName: "global-service",
		},
		{
			name: "Test SUBSCRIBE command with timeout",
			opts: []Option{
				WithRawCommand(true),
			},
			runTest: func(t *testing.T, ctx context.Context, client rueidis.Client) {
				ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Millisecond)
				require.EqualError(t,
					context.DeadlineExceeded,
					client.Receive(ctxWithTimeout, client.B().Subscribe().Channel("test_channel").Build(), func(_ rueidis.PubSubMessage) {}).Error(),
				)
				cancel()
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1)

				span := spans[0]
				assert.Equal(t, "SUBSCRIBE", span.Tag(ext.ResourceName))
				assert.Equal(t, "SUBSCRIBE test_channel", span.Tag(ext.RedisRawCommand))
				assert.Nil(t, span.Tag(ext.RedisClientCacheHit))
				assert.Nil(t, span.Tag(ext.RedisClientCacheTTL))
				assert.Nil(t, span.Tag(ext.RedisClientCachePXAT))
				assert.Nil(t, span.Tag(ext.RedisClientCachePTTL))
				assert.Equal(t, context.DeadlineExceeded.Error(), span.Tag(ext.ErrorMsg))
			},
			wantServiceName: "global-service",
		},
		{
			name: "Test Dedicated client",
			opts: []Option{
				WithRawCommand(true),
			},
			runTest: func(t *testing.T, ctx context.Context, client rueidis.Client) {
				err := client.Dedicated(func(d rueidis.DedicatedClient) error {
					return d.Do(ctx, client.B().Set().Key("test_key").Value("test_value").Build()).Error()
				})
				require.NoError(t, err)
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1)

				span := spans[0]
				assert.Equal(t, "SET", span.Tag(ext.ResourceName))
				assert.Equal(t, "SET test_key test_value", span.Tag(ext.RedisRawCommand))
				assert.Equal(t, "false", span.Tag(ext.RedisClientCacheHit))
				assert.Less(t, span.Tag(ext.RedisClientCacheTTL), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePXAT), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePTTL), float64(0))
				assert.Nil(t, span.Tag(ext.Error))
			},
			wantServiceName: "global-service",
		},
		{
			name: "Test SET command with canceled context and custom error check",
			opts: []Option{
				WithErrorCheck(func(err error) bool {
					return err != nil && !rueidis.IsRedisNil(err) && !errors.Is(err, context.Canceled)
				}),
			},
			runTest: func(t *testing.T, ctx context.Context, client rueidis.Client) {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				require.Error(t, client.Do(ctx, client.B().Set().Key("test_key").Value("test_value").Build()).Error())
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1)

				span := spans[0]
				assert.Equal(t, "SET", span.Tag(ext.ResourceName))
				assert.Nil(t, span.Tag(ext.RedisRawCommand))
				assert.Equal(t, "false", span.Tag(ext.RedisClientCacheHit))
				assert.Less(t, span.Tag(ext.RedisClientCacheTTL), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePXAT), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePTTL), float64(0))
				assert.Nil(t, span.Tag(ext.Error))
			},
			wantServiceName: "global-service",
		},
		{
			name: "Test redis nil not attached to span",
			opts: []Option{
				WithRawCommand(true),
			},
			runTest: func(t *testing.T, ctx context.Context, client rueidis.Client) {
				require.Error(t, client.Do(ctx, client.B().Get().Key("404").Build()).Error())
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1)

				span := spans[0]
				assert.Equal(t, "GET", span.Tag(ext.ResourceName))
				assert.Equal(t, "GET 404", span.Tag(ext.RedisRawCommand))
				assert.Equal(t, "false", span.Tag(ext.RedisClientCacheHit))
				assert.Less(t, span.Tag(ext.RedisClientCacheTTL), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePXAT), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePTTL), float64(0))
				assert.Nil(t, span.Tag(ext.Error))
			},
			wantServiceName: "global-service",
		},
		{
			name: "Test SET command with canceled context and custom error check",
			opts: []Option{
				WithErrorCheck(func(err error) bool {
					return err != nil && !rueidis.IsRedisNil(err) && !errors.Is(err, context.Canceled)
				}),
			},
			runTest: func(t *testing.T, ctx context.Context, client rueidis.Client) {
				ctx, cancel := context.WithCancel(ctx)
				cancel()
				require.Error(t, client.Do(ctx, client.B().Set().Key("test_key").Value("test_value").Build()).Error())
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1)

				span := spans[0]
				assert.Equal(t, "SET", span.Tag(ext.ResourceName))
				assert.Nil(t, span.Tag(ext.RedisRawCommand))
				assert.Equal(t, "false", span.Tag(ext.RedisClientCacheHit))
				assert.Less(t, span.Tag(ext.RedisClientCacheTTL), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePXAT), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePTTL), float64(0))
				assert.Nil(t, span.Tag(ext.Error))
			},
			wantServiceName: "global-service",
		},
		{
			name: "Test redis nil not attached to span",
			opts: []Option{
				WithRawCommand(true),
			},
			runTest: func(t *testing.T, ctx context.Context, client rueidis.Client) {
				require.Error(t, client.Do(ctx, client.B().Get().Key("404").Build()).Error())
			},
			assertSpans: func(t *testing.T, spans []*mocktracer.Span) {
				require.Len(t, spans, 1)

				span := spans[0]
				assert.Equal(t, "GET", span.Tag(ext.ResourceName))
				assert.Equal(t, "GET 404", span.Tag(ext.RedisRawCommand))
				assert.Equal(t, "false", span.Tag(ext.RedisClientCacheHit))
				assert.Less(t, span.Tag(ext.RedisClientCacheTTL), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePXAT), float64(0))
				assert.Less(t, span.Tag(ext.RedisClientCachePTTL), float64(0))
				assert.Nil(t, span.Tag(ext.Error))
			},
			wantServiceName: "global-service",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			rueidisClientOption := rueidis.ClientOption{
				InitAddress:  redisAddrs,
				DisableCache: true,
			}
			client, err := NewClient(rueidisClientOption, tt.opts...)
			require.NoError(t, err)

			root, ctx := tracer.StartSpanFromContext(context.Background(), "test.root", tracer.ServiceName("test-service"))
			tt.runTest(t, ctx, client)
			root.Finish() // test.root exists in the last span.

			spans := mt.FinishedSpans()
			tt.assertSpans(t, spans[:len(spans)-1])

			for _, span := range spans {
				if span.OperationName() == "test.root" {
					continue
				}
				// The following assertions are common to all spans
				assert.Equal(t, tt.wantServiceName, span.Tag(ext.ServiceName))
				assert.Equal(t, "127.0.0.1", span.Tag(ext.TargetHost))
				assert.Equal(t, "6379", span.Tag(ext.TargetPort))
				assert.Equal(t, "0", span.Tag(ext.TargetDB))
				assert.Equal(t, "redis.command", span.OperationName())
				assert.Equal(t, "client", span.Tag(ext.SpanKind))
				assert.Equal(t, "redis", span.Tag(ext.SpanType))
				assert.Equal(t, "redis/rueidis", span.Tag(ext.Component))
				assert.Equal(t, "redis", span.Tag(ext.DBSystem))
			}
		})
	}

}
