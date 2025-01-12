// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package valkey_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
	valkeytrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/valkey-go"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
)

var (
	// See docker-compose.yaml
	valkeyPort     = 6380
	valkeyUsername = "default"
	valkeyPassword = "password-for-default"
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
	tests := []struct {
		name                     string
		valkeyClientOptions      valkey.ClientOption
		valkeytraceClientOptions []valkeytrace.ClientOption
		valkeytraceClientEnvVars map[string]string
		createSpans              func(*testing.T, context.Context, valkey.Client)
		assertNewClientError     func(*testing.T, error)
		assertSpans              []func(*testing.T, mocktracer.Span)
	}{
		{
			name: "Test invalid username",
			valkeyClientOptions: valkey.ClientOption{
				InitAddress: []string{fmt.Sprintf("127.0.0.1:%d", valkeyPort)},
				Username:    "invalid-username",
				Password:    valkeyPassword,
			},
			assertNewClientError: func(t *testing.T, err error) {
				assert.EqualError(t, err, "WRONGPASS invalid username-password pair or user is disabled.")
			},
		},
		{
			name: "Test invalid password",
			valkeyClientOptions: valkey.ClientOption{
				InitAddress: []string{fmt.Sprintf("127.0.0.1:%d", valkeyPort)},
				Username:    valkeyUsername,
				Password:    "invalid",
			},
			assertNewClientError: func(t *testing.T, err error) {
				assert.EqualError(t, err, "WRONGPASS invalid username-password pair or user is disabled.")
			},
		},
		{
			name: "Test SET command with custom options",
			valkeyClientOptions: valkey.ClientOption{
				InitAddress: []string{fmt.Sprintf("127.0.0.1:%d", valkeyPort)},
				Username:    valkeyUsername,
				Password:    valkeyPassword,
			},
			valkeytraceClientOptions: []valkeytrace.ClientOption{
				valkeytrace.WithServiceName("my-valkey-client"),
				valkeytrace.WithAnalytics(true),
				valkeytrace.WithSkipRawCommand(true),
			},
			createSpans: func(t *testing.T, ctx context.Context, client valkey.Client) {
				assert.NoError(t, client.Do(ctx, client.B().Set().Key("test_key").Value("test_value").Build()).Error())
			},
			assertSpans: []func(t *testing.T, span mocktracer.Span){
				func(t *testing.T, span mocktracer.Span) {
					assert.Equal(t, "my-valkey-client", span.Tag(ext.ServiceName))
					assert.Equal(t, "127.0.0.1", span.Tag(ext.TargetHost))
					assert.Equal(t, valkeyPort, span.Tag(ext.TargetPort))
					assert.Equal(t, "SET", span.Tag(ext.DBStatement))
					assert.Equal(t, "SET", span.Tag(ext.ResourceName))
					assert.Greater(t, span.Tag("db.stmt_size"), 0)
					assert.Equal(t, "SET", span.Tag("db.operation"))
					assert.True(t, span.Tag(ext.ValkeyClientCommandWrite).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandStream).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandBlock).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandMulti).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCacheHit).(bool))
					assert.Less(t, span.Tag(ext.ValkeyClientCacheTTL), int64(0))
					assert.Less(t, span.Tag(ext.ValkeyClientCachePXAT), int64(0))
					assert.Less(t, span.Tag(ext.ValkeyClientCachePTTL), int64(0))
					assert.Nil(t, span.Tag(ext.DBApplication))
					assert.Equal(t, 1.0, span.Tag(ext.EventSampleRate))
					assert.Nil(t, span.Tag(ext.Error))
				},
			},
		},
		{
			name: "Test SET/GET commands",
			valkeyClientOptions: valkey.ClientOption{
				InitAddress: []string{fmt.Sprintf("127.0.0.1:%d", valkeyPort)},
				Username:    valkeyUsername,
				Password:    valkeyPassword,
				ClientName:  "my-valkey-client",
			},
			createSpans: func(t *testing.T, ctx context.Context, client valkey.Client) {
				resp := client.DoMulti(ctx, client.B().Set().Key("test_key").Value("test_value").Build(), client.B().Get().Key("test_key").Build())
				assert.Len(t, resp, 2)
			},
			assertSpans: []func(t *testing.T, span mocktracer.Span){
				func(t *testing.T, span mocktracer.Span) {
					assert.Equal(t, "valkey.client", span.Tag(ext.ServiceName))
					assert.Equal(t, "127.0.0.1", span.Tag(ext.TargetHost))
					assert.Equal(t, valkeyPort, span.Tag(ext.TargetPort))
					assert.Equal(t, "SET\ntest_key\ntest_value\nGET\ntest_key", span.Tag(ext.DBStatement))
					assert.Equal(t, "SET\ntest_key\ntest_value\nGET\ntest_key", span.Tag(ext.ResourceName))
					assert.Greater(t, span.Tag("db.stmt_size"), 0)
					assert.Equal(t, "SET GET", span.Tag("db.operation"))
					assert.False(t, span.Tag(ext.ValkeyClientCommandWrite).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandStream).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandBlock).(bool))
					assert.True(t, span.Tag(ext.ValkeyClientCommandMulti).(bool))
					assert.Nil(t, span.Tag(ext.ValkeyClientCacheHit))
					assert.Nil(t, span.Tag(ext.ValkeyClientCacheTTL))
					assert.Nil(t, span.Tag(ext.ValkeyClientCachePXAT))
					assert.Nil(t, span.Tag(ext.ValkeyClientCachePTTL))
					assert.Equal(t, "my-valkey-client", span.Tag(ext.DBApplication))
					assert.Nil(t, span.Tag(ext.EventSampleRate))
					assert.Nil(t, span.Tag(ext.Error))
				},
			},
		},
		{
			name: "Test HMGET command with cache",
			valkeyClientOptions: valkey.ClientOption{
				InitAddress: []string{fmt.Sprintf("127.0.0.1:%d", valkeyPort)},
				Username:    valkeyUsername,
				Password:    valkeyPassword,
			},
			createSpans: func(t *testing.T, ctx context.Context, client valkey.Client) {
				assert.NoError(t, client.DoCache(ctx, client.B().Hmget().Key("mk").Field("1", "2").Cache(), time.Minute).Error())
				resp, err := client.DoCache(ctx, client.B().Hmget().Key("mk").Field("1", "2").Cache(), time.Minute).ToArray()
				assert.Len(t, resp, 2)
				assert.NoError(t, err)
			},
			assertSpans: []func(t *testing.T, span mocktracer.Span){
				func(t *testing.T, span mocktracer.Span) {
					assert.Equal(t, "valkey.client", span.Tag(ext.ServiceName))
					assert.Equal(t, "127.0.0.1", span.Tag(ext.TargetHost))
					assert.Equal(t, valkeyPort, span.Tag(ext.TargetPort))
					assert.Greater(t, span.Tag("db.stmt_size"), 0)
					assert.Equal(t, "HMGET\nmk\n1\n2", span.Tag(ext.DBStatement))
					assert.Equal(t, "HMGET\nmk\n1\n2", span.Tag(ext.ResourceName))
					assert.Equal(t, "HMGET", span.Tag("db.operation"))
					assert.False(t, span.Tag(ext.ValkeyClientCommandWrite).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCacheHit).(bool))
					assert.Greater(t, span.Tag(ext.ValkeyClientCacheTTL), int64(0))
					assert.Greater(t, span.Tag(ext.ValkeyClientCachePXAT), int64(0))
					assert.Greater(t, span.Tag(ext.ValkeyClientCachePTTL), int64(0))
					assert.False(t, span.Tag(ext.ValkeyClientCommandStream).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandBlock).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandMulti).(bool))
					assert.Nil(t, span.Tag(ext.DBApplication))
					assert.Nil(t, span.Tag(ext.EventSampleRate))
					assert.Nil(t, span.Tag(ext.Error))
				},
				func(t *testing.T, span mocktracer.Span) {
					assert.Equal(t, "valkey.client", span.Tag(ext.ServiceName))
					assert.Equal(t, "127.0.0.1", span.Tag(ext.TargetHost))
					assert.Equal(t, valkeyPort, span.Tag(ext.TargetPort))
					assert.Greater(t, span.Tag("db.stmt_size"), 0)
					assert.Equal(t, "HMGET\nmk\n1\n2", span.Tag(ext.DBStatement))
					assert.Equal(t, "HMGET\nmk\n1\n2", span.Tag(ext.ResourceName))
					assert.Equal(t, "HMGET", span.Tag("db.operation"))
					assert.False(t, span.Tag(ext.ValkeyClientCommandWrite).(bool))
					assert.True(t, span.Tag(ext.ValkeyClientCacheHit).(bool))
					assert.Greater(t, span.Tag(ext.ValkeyClientCacheTTL), int64(0))
					assert.Greater(t, span.Tag(ext.ValkeyClientCachePXAT), int64(0))
					assert.Greater(t, span.Tag(ext.ValkeyClientCachePTTL), int64(0))
					assert.False(t, span.Tag(ext.ValkeyClientCommandStream).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandBlock).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandMulti).(bool))
					assert.Nil(t, span.Tag(ext.DBApplication))
					assert.Nil(t, span.Tag(ext.EventSampleRate))
					assert.Nil(t, span.Tag(ext.Error))
				},
			},
		},
		{
			name: "Test GET command with stream with env vars",
			valkeyClientOptions: valkey.ClientOption{
				InitAddress: []string{fmt.Sprintf("127.0.0.1:%d", valkeyPort)},
				Username:    valkeyUsername,
				Password:    valkeyPassword,
			},
			valkeytraceClientEnvVars: map[string]string{
				"DD_TRACE_VALKEY_SERVICE_NAME":      "my-valkey-client",
				"DD_TRACE_VALKEY_ANALYTICS_ENABLED": "true",
				"DD_TRACE_VALKEY_SKIP_RAW_COMMAND":  "true",
			},
			createSpans: func(t *testing.T, ctx context.Context, client valkey.Client) {
				resp := client.DoStream(ctx, client.B().Get().Key("test_key").Build())
				assert.NoError(t, resp.Error())
			},
			assertSpans: []func(t *testing.T, span mocktracer.Span){
				func(t *testing.T, span mocktracer.Span) {
					assert.Equal(t, "my-valkey-client", span.Tag(ext.ServiceName))
					assert.Equal(t, "127.0.0.1", span.Tag(ext.TargetHost))
					assert.Equal(t, valkeyPort, span.Tag(ext.TargetPort))
					assert.Equal(t, "GET", span.Tag(ext.DBStatement))
					assert.Equal(t, "GET", span.Tag(ext.ResourceName))
					assert.Greater(t, span.Tag("db.stmt_size"), 0)
					assert.Equal(t, "GET", span.Tag("db.operation"))
					assert.False(t, span.Tag(ext.ValkeyClientCommandWrite).(bool))
					assert.True(t, span.Tag(ext.ValkeyClientCommandStream).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandBlock).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandMulti).(bool))
					assert.Nil(t, span.Tag(ext.ValkeyClientCacheHit))
					assert.Nil(t, span.Tag(ext.ValkeyClientCacheTTL))
					assert.Nil(t, span.Tag(ext.ValkeyClientCachePXAT))
					assert.Nil(t, span.Tag(ext.ValkeyClientCachePTTL))
					assert.Nil(t, span.Tag(ext.DBApplication))
					assert.Equal(t, 1.0, span.Tag(ext.EventSampleRate))
					assert.Nil(t, span.Tag(ext.Error))
				},
			},
		},
		{
			name: "Test SUBSCRIBE command with timeout",
			valkeyClientOptions: valkey.ClientOption{
				InitAddress: []string{fmt.Sprintf("127.0.0.1:%d", valkeyPort)},
				Username:    valkeyUsername,
				Password:    valkeyPassword,
			},
			createSpans: func(t *testing.T, ctx context.Context, client valkey.Client) {
				ctxWithTimeout, cancel := context.WithTimeout(ctx, time.Millisecond)
				assert.Equal(t,
					context.DeadlineExceeded,
					client.Receive(ctxWithTimeout, client.B().Subscribe().Channel("test_channel").Build(), func(msg valkey.PubSubMessage) {}),
				)
				cancel()
			},
			assertSpans: []func(t *testing.T, span mocktracer.Span){
				func(t *testing.T, span mocktracer.Span) {
					assert.Equal(t, "valkey.client", span.Tag(ext.ServiceName))
					assert.Equal(t, "127.0.0.1", span.Tag(ext.TargetHost))
					assert.Equal(t, valkeyPort, span.Tag(ext.TargetPort))
					assert.Greater(t, span.Tag("db.stmt_size"), 0)
					assert.Equal(t, "SUBSCRIBE\ntest_channel", span.Tag(ext.DBStatement))
					assert.Equal(t, "SUBSCRIBE\ntest_channel", span.Tag(ext.ResourceName))
					assert.Equal(t, "SUBSCRIBE", span.Tag("db.operation"))
					assert.False(t, span.Tag(ext.ValkeyClientCommandWrite).(bool))
					assert.Nil(t, span.Tag(ext.ValkeyClientCacheHit))
					assert.Nil(t, span.Tag(ext.ValkeyClientCacheTTL))
					assert.Nil(t, span.Tag(ext.ValkeyClientCachePXAT))
					assert.Nil(t, span.Tag(ext.ValkeyClientCachePTTL))
					assert.False(t, span.Tag(ext.ValkeyClientCommandStream).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandBlock).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCommandMulti).(bool))
					assert.Nil(t, span.Tag(ext.DBApplication))
					assert.Nil(t, span.Tag(ext.EventSampleRate))
					assert.Equal(t, context.DeadlineExceeded, span.Tag(ext.Error).(error))
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mt := mocktracer.Start()
			defer mt.Stop()
			for k, v := range tt.valkeytraceClientEnvVars {
				t.Setenv(k, v)
			}
			client, err := valkeytrace.NewClient(tt.valkeyClientOptions, tt.valkeytraceClientOptions...)
			if tt.assertNewClientError == nil {
				require.NoErrorf(t, err, tt.name)
			} else {
				tt.assertNewClientError(t, err)
				return
			}
			tt.createSpans(t, ctx, client)
			spans := mt.FinishedSpans()
			require.Len(t, spans, len(tt.assertSpans))
			for i, span := range spans {
				tt.assertSpans[i](t, span)
				// Following assertions are common to all spans
				assert.NotNil(t, span)
				assert.True(t, span.Tag(ext.ValkeyClientCommandWithPassword).(bool))
				assert.Equal(t, tt.valkeyClientOptions.Username, span.Tag(ext.DBUser))
				assert.Equal(t, "valkey.command", span.OperationName())
				assert.Equal(t, "client", span.Tag(ext.SpanKind))
				assert.Equal(t, ext.SpanTypeValkey, span.Tag(ext.SpanType))
				assert.Equal(t, "valkey-go/valkey", span.Tag(ext.Component))
				assert.Equal(t, "valkey", span.Tag(ext.DBType))
				assert.Equal(t, "valkey", span.Tag(ext.DBSystem))
			}
		})
	}

}
