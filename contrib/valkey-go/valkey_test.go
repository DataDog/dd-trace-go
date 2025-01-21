// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package valkey

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
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

func TestPeerTags(t *testing.T) {
	tests := []struct {
		initAddress  string
		expectedTags map[string]interface{}
	}{
		{
			initAddress: "127.0.0.1:6379",
			expectedTags: map[string]interface{}{
				ext.PeerService:  "valkey",
				ext.PeerHostIPV4: "127.0.0.1",
				ext.PeerPort:     6379,
			},
		},
		{
			initAddress: "[::1]:6379",
			expectedTags: map[string]interface{}{
				ext.PeerService:  "valkey",
				ext.PeerHostIPV6: "::1",
				ext.PeerPort:     6379,
			},
		},
		{
			initAddress: "[2001:db8::2]:6379",
			expectedTags: map[string]interface{}{
				ext.PeerService:  "valkey",
				ext.PeerHostIPV6: "2001:db8::2",
				ext.PeerPort:     6379,
			},
		},
		{
			initAddress: "[2001:db8::2%lo]:6379",
			expectedTags: map[string]interface{}{
				ext.PeerService:  "valkey",
				ext.PeerHostname: "2001:db8::2%lo",
				ext.PeerPort:     6379,
			},
		},
		{
			initAddress: "::1:7777",
			expectedTags: map[string]interface{}{
				ext.PeerService:  "valkey",
				ext.PeerHostname: "",
				ext.PeerPort:     0,
			},
		},
		{
			initAddress: ":::7777",
			expectedTags: map[string]interface{}{
				ext.PeerService:  "valkey",
				ext.PeerHostname: "",
				ext.PeerPort:     0,
			},
		},
		{
			initAddress: "localhost:7777",
			expectedTags: map[string]interface{}{
				ext.PeerService:  "valkey",
				ext.PeerHostname: "localhost",
				ext.PeerPort:     7777,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.initAddress, func(t *testing.T) {
			host, port := splitHostPort(tt.initAddress)
			client := coreClient{
				host: host,
				port: port,
			}
			var startSpanConfig ddtrace.StartSpanConfig
			for _, tag := range client.peerTags() {
				tag(&startSpanConfig)
			}
			require.Equal(t, tt.expectedTags, startSpanConfig.Tags)
		})
	}
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		name                     string
		valkeyClientOptions      valkey.ClientOption
		valkeytraceClientOptions []ClientOption
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
			valkeytraceClientOptions: []ClientOption{
				WithSkipRawCommand(true),
			},
			createSpans: func(t *testing.T, ctx context.Context, client valkey.Client) {
				assert.NoError(t, client.Do(ctx, client.B().Set().Key("test_key").Value("test_value").Build()).Error())
			},
			assertSpans: []func(t *testing.T, span mocktracer.Span){
				func(t *testing.T, span mocktracer.Span) {
					assert.Equal(t, "SET\ntest_key\ntest_value", span.Tag(ext.DBStatement))
					assert.Equal(t, "SET\ntest_key\ntest_value", span.Tag(ext.ResourceName))
					assert.Greater(t, span.Tag("db.stmt_size"), 0)
					assert.Equal(t, "SET", span.Tag("db.operation"))
					assert.True(t, span.Tag(ext.ValkeyRawCommand).(bool))
					assert.False(t, span.Tag(ext.ValkeyClientCacheHit).(bool))
					assert.Less(t, span.Tag(ext.ValkeyClientCacheTTL), int64(0))
					assert.Less(t, span.Tag(ext.ValkeyClientCachePXAT), int64(0))
					assert.Less(t, span.Tag(ext.ValkeyClientCachePTTL), int64(0))
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
					assert.Equal(t, "SET\ntest_key\ntest_value\nGET\ntest_key", span.Tag(ext.DBStatement))
					assert.Equal(t, "SET\ntest_key\ntest_value\nGET\ntest_key", span.Tag(ext.ResourceName))
					assert.Greater(t, span.Tag("db.stmt_size"), 0)
					assert.Equal(t, "SET GET", span.Tag("db.operation"))
					assert.Nil(t, span.Tag(ext.ValkeyRawCommand))
					assert.Nil(t, span.Tag(ext.ValkeyClientCacheHit))
					assert.Nil(t, span.Tag(ext.ValkeyClientCacheTTL))
					assert.Nil(t, span.Tag(ext.ValkeyClientCachePXAT))
					assert.Nil(t, span.Tag(ext.ValkeyClientCachePTTL))
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
					assert.Greater(t, span.Tag("db.stmt_size"), 0)
					assert.Equal(t, "HMGET\nmk\n1\n2", span.Tag(ext.DBStatement))
					assert.Equal(t, "HMGET\nmk\n1\n2", span.Tag(ext.ResourceName))
					assert.Equal(t, "HMGET", span.Tag("db.operation"))
					assert.False(t, span.Tag(ext.ValkeyClientCacheHit).(bool))
					assert.Greater(t, span.Tag(ext.ValkeyClientCacheTTL), int64(0))
					assert.Greater(t, span.Tag(ext.ValkeyClientCachePXAT), int64(0))
					assert.Greater(t, span.Tag(ext.ValkeyClientCachePTTL), int64(0))
					assert.Nil(t, span.Tag(ext.Error))
				},
				func(t *testing.T, span mocktracer.Span) {
					assert.Greater(t, span.Tag("db.stmt_size"), 0)
					assert.Equal(t, "HMGET\nmk\n1\n2", span.Tag(ext.DBStatement))
					assert.Equal(t, "HMGET\nmk\n1\n2", span.Tag(ext.ResourceName))
					assert.Equal(t, "HMGET", span.Tag("db.operation"))
					assert.Nil(t, span.Tag(ext.ValkeyRawCommand))
					assert.True(t, span.Tag(ext.ValkeyClientCacheHit).(bool))
					assert.Greater(t, span.Tag(ext.ValkeyClientCacheTTL), int64(0))
					assert.Greater(t, span.Tag(ext.ValkeyClientCachePXAT), int64(0))
					assert.Greater(t, span.Tag(ext.ValkeyClientCachePTTL), int64(0))
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
				"DD_TRACE_VALKEY_SKIP_RAW_COMMAND": "true",
			},
			createSpans: func(t *testing.T, ctx context.Context, client valkey.Client) {
				resp := client.DoStream(ctx, client.B().Get().Key("test_key").Build())
				assert.NoError(t, resp.Error())
			},
			assertSpans: []func(t *testing.T, span mocktracer.Span){
				func(t *testing.T, span mocktracer.Span) {
					assert.Equal(t, "GET\ntest_key", span.Tag(ext.DBStatement))
					assert.Equal(t, "GET\ntest_key", span.Tag(ext.ResourceName))
					assert.Greater(t, span.Tag("db.stmt_size"), 0)
					assert.Equal(t, "GET", span.Tag("db.operation"))
					assert.True(t, span.Tag(ext.ValkeyRawCommand).(bool))
					assert.Nil(t, span.Tag(ext.ValkeyClientCacheHit))
					assert.Nil(t, span.Tag(ext.ValkeyClientCacheTTL))
					assert.Nil(t, span.Tag(ext.ValkeyClientCachePXAT))
					assert.Nil(t, span.Tag(ext.ValkeyClientCachePTTL))
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
					assert.Greater(t, span.Tag("db.stmt_size"), 0)
					assert.Equal(t, "SUBSCRIBE\ntest_channel", span.Tag(ext.DBStatement))
					assert.Equal(t, "SUBSCRIBE\ntest_channel", span.Tag(ext.ResourceName))
					assert.Equal(t, "SUBSCRIBE", span.Tag("db.operation"))
					assert.Nil(t, span.Tag(ext.ValkeyRawCommand))
					assert.Nil(t, span.Tag(ext.ValkeyClientCacheHit))
					assert.Nil(t, span.Tag(ext.ValkeyClientCacheTTL))
					assert.Nil(t, span.Tag(ext.ValkeyClientCachePXAT))
					assert.Nil(t, span.Tag(ext.ValkeyClientCachePTTL))
					assert.Equal(t, context.DeadlineExceeded, span.Tag(ext.Error).(error))
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			for k, v := range tt.valkeytraceClientEnvVars {
				t.Setenv(k, v)
			}
			span, ctx := tracer.StartSpanFromContext(context.Background(), "test.root", tracer.ServiceName("test-service"))
			client, err := NewClient(tt.valkeyClientOptions, tt.valkeytraceClientOptions...)
			if tt.assertNewClientError == nil {
				require.NoErrorf(t, err, tt.name)
			} else {
				tt.assertNewClientError(t, err)
				span.Finish()
				return
			}
			tt.createSpans(t, ctx, client)
			span.Finish() // test.root exists in the last span.
			spans := mt.FinishedSpans()
			require.Len(t, spans, len(tt.assertSpans)+1) // +1 for test.root
			for i, span := range spans {
				if span.OperationName() == "test.root" {
					continue
				}
				tt.assertSpans[i](t, span)
				t.Log("Following assertions are common to all spans")
				assert.Equalf(t,
					"test-service",
					span.Tag(ext.ServiceName),
					"service name should not be overwritten as per DD_APM_PEER_TAGS_AGGREGATION in trace-agent",
				)
				assert.Equal(t, "valkey", span.Tag(ext.PeerService))
				assert.Equal(t, "127.0.0.1", span.Tag(ext.PeerHostIPV4))
				assert.Equal(t, "127.0.0.1", span.Tag(ext.TargetHost))
				assert.Equal(t, valkeyPort, span.Tag(ext.PeerPort))
				assert.Equal(t, valkeyPort, span.Tag(ext.TargetPort))
				assert.NotNil(t, span)
				assert.Equal(t, tt.valkeyClientOptions.Username, span.Tag(ext.DBUser))
				assert.Equal(t, "valkey.command", span.OperationName())
				assert.Equal(t, "client", span.Tag(ext.SpanKind))
				assert.Equal(t, ext.SpanTypeValkey, span.Tag(ext.SpanType))
				assert.Equal(t, "valkey-go/valkey", span.Tag(ext.Component))
				assert.Equal(t, "valkey", span.Tag(ext.DBSystem))
			}
		})
	}

}
