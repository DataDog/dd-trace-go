// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
)

const debug = false

// ensure it's a redis.Hook
var _ redis.Hook = (*datadogHook)(nil)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestClientEvalSha(t *testing.T) {
	ctx := context.Background()
	opts := &redis.Options{Addr: "127.0.0.1:6379"}
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := NewClient(opts, WithServiceName("my-redis"))

	sha1 := client.ScriptLoad(ctx, "return {KEYS[1],KEYS[2],ARGV[1],ARGV[2]}").Val()
	mt.Reset()

	client.EvalSha(ctx, sha1, []string{"key1", "key2", "first", "second"})

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	assert.Equal("redis.command", span.OperationName())
	assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
	assert.Equal("my-redis", span.Tag(ext.ServiceName))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("evalsha", span.Tag(ext.ResourceName))
}

func TestClient(t *testing.T) {
	ctx := context.Background()
	opts := &redis.Options{Addr: "127.0.0.1:6379"}
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := NewClient(opts, WithServiceName("my-redis"))
	client.Set(ctx, "test_key", "test_value", 0)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	assert.Equal("redis.command", span.OperationName())
	assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
	assert.Equal("my-redis", span.Tag(ext.ServiceName))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("set test_key test_value: ", span.Tag("redis.raw_command"))
	assert.Equal("3", span.Tag("redis.args_length"))
}

func TestWrapClient(t *testing.T) {
	simpleClientOpts := &redis.UniversalOptions{Addrs: []string{"127.0.0.1:6379"}}
	simpleClient := redis.NewUniversalClient(simpleClientOpts)

	failoverClientOpts := &redis.UniversalOptions{
		MasterName: "leader.redis.host",
		Addrs: []string{
			"127.0.0.1:6379",
			"127.0.0.2:6379",
		}}
	failoverClient := redis.NewUniversalClient(failoverClientOpts)

	clusterClientOpts := &redis.UniversalOptions{
		Addrs: []string{
			"127.0.0.1:6379",
			"127.0.0.2:6379",
		},
		DialTimeout: 1}
	clusterClient := redis.NewUniversalClient(clusterClientOpts)

	testCases := []struct {
		name   string
		client redis.UniversalClient
	}{
		{
			name:   "simple-client",
			client: simpleClient,
		},
		{
			name:   "failover-client",
			client: failoverClient,
		},
		{
			name:   "cluster-client",
			client: clusterClient,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			assert := assert.New(t)
			mt := mocktracer.Start()
			defer mt.Stop()

			WrapClient(tc.client, WithServiceName("my-redis"))
			tc.client.Set(ctx, "test_key", "test_value", 0)

			spans := mt.FinishedSpans()
			assert.Len(spans, 1)

			span := spans[0]
			assert.Equal("redis.command", span.OperationName())
			assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
			assert.Equal("my-redis", span.Tag(ext.ServiceName))
			assert.Equal("set test_key test_value: ", span.Tag("redis.raw_command"))
			assert.Equal("3", span.Tag("redis.args_length"))
		})
	}
}

func TestAdditionalTagsFromClient(t *testing.T) {
	t.Run("simple-client", func(t *testing.T) {
		simpleClientOpts := &redis.UniversalOptions{Addrs: []string{"127.0.0.1:6379"}}
		simpleClient := redis.NewUniversalClient(simpleClientOpts)
		config := &ddtrace.StartSpanConfig{}
		expectedTags := map[string]interface{}{
			"out.db":   "0",
			"out.host": "127.0.0.1",
			"out.port": "6379",
		}

		additionalTagOptions := additionalTagOptions(simpleClient)
		for _, t := range additionalTagOptions {
			t(config)
		}
		assert.Equal(t, expectedTags, config.Tags)
	})

	t.Run("failover-client", func(t *testing.T) {
		failoverClientOpts := &redis.UniversalOptions{
			MasterName: "leader.redis.host",
			Addrs: []string{
				"127.0.0.1:6379",
				"127.0.0.2:6379",
			}}
		failoverClient := redis.NewUniversalClient(failoverClientOpts)
		config := &ddtrace.StartSpanConfig{}
		expectedTags := map[string]interface{}{
			"out.db": "0",
		}

		additionalTagOptions := additionalTagOptions(failoverClient)
		for _, t := range additionalTagOptions {
			t(config)
		}
		assert.Equal(t, expectedTags, config.Tags)
	})

	t.Run("cluster-client", func(t *testing.T) {
		clusterClientOpts := &redis.UniversalOptions{
			Addrs: []string{
				"127.0.0.1:6379",
				"127.0.0.2:6379",
			},
			DialTimeout: 1}
		clusterClient := redis.NewUniversalClient(clusterClientOpts)
		config := &ddtrace.StartSpanConfig{}
		expectedTags := map[string]interface{}{
			"addrs": "127.0.0.1:6379, 127.0.0.2:6379",
		}

		additionalTagOptions := additionalTagOptions(clusterClient)
		for _, t := range additionalTagOptions {
			t(config)
		}
		assert.Equal(t, expectedTags, config.Tags)
	})
}

func TestPipeline(t *testing.T) {
	ctx := context.Background()
	opts := &redis.Options{Addr: "127.0.0.1:6379"}
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := NewClient(opts, WithServiceName("my-redis"))
	pipeline := client.Pipeline()
	pipeline.Expire(ctx, "pipeline_counter", time.Hour)

	// Exec with context test
	pipeline.Exec(ctx)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	assert.Equal("redis.command", span.OperationName())
	assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
	assert.Equal("my-redis", span.Tag(ext.ServiceName))
	assert.Equal("expire pipeline_counter 3600: false\n", span.Tag(ext.ResourceName))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("1", span.Tag("redis.pipeline_length"))

	mt.Reset()
	pipeline.Expire(ctx, "pipeline_counter", time.Hour)
	pipeline.Expire(ctx, "pipeline_counter_1", time.Minute)

	// Rewriting Exec
	pipeline.Exec(ctx)

	spans = mt.FinishedSpans()
	assert.Len(spans, 1)

	span = spans[0]
	assert.Equal("redis.command", span.OperationName())
	assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
	assert.Equal("my-redis", span.Tag(ext.ServiceName))
	assert.Equal("expire pipeline_counter 3600: false\nexpire pipeline_counter_1 60: false\n", span.Tag(ext.ResourceName))
	assert.Equal("2", span.Tag("redis.pipeline_length"))
}

func TestChildSpan(t *testing.T) {
	ctx := context.Background()
	opts := &redis.Options{Addr: "127.0.0.1:6379"}
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	// Parent span
	client := NewClient(opts, WithServiceName("my-redis"))
	root, ctx := tracer.StartSpanFromContext(ctx, "parent.span")

	client.Set(ctx, "test_key", "test_value", 0)
	root.Finish()

	spans := mt.FinishedSpans()
	assert.Len(spans, 2)

	var child, parent mocktracer.Span
	for _, s := range spans {
		// order of traces in buffer is not garanteed
		switch s.OperationName() {
		case "redis.command":
			child = s
		case "parent.span":
			parent = s
		}
	}
	assert.NotNil(parent)
	assert.NotNil(child)

	assert.Equal(child.ParentID(), parent.SpanID())
	assert.Equal(child.Tag(ext.TargetHost), "127.0.0.1")
	assert.Equal(child.Tag(ext.TargetPort), "6379")
}

func TestMultipleCommands(t *testing.T) {
	ctx := context.Background()
	opts := &redis.Options{Addr: "127.0.0.1:6379"}
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := NewClient(opts, WithServiceName("my-redis"))
	client.Set(ctx, "test_key", "test_value", 0)
	client.Get(ctx, "test_key")
	client.Incr(ctx, "int_key")
	client.ClientList(ctx)

	spans := mt.FinishedSpans()
	assert.Len(spans, 4)

	// Checking all commands were recorded
	var commands [4]string
	for i := 0; i < 4; i++ {
		commands[i] = spans[i].Tag("redis.raw_command").(string)
	}
	assert.Contains(commands, "set test_key test_value: ")
	assert.Contains(commands, "get test_key: ")
	assert.Contains(commands, "incr int_key: 0")
	assert.Contains(commands, "client list: ")
}

func TestError(t *testing.T) {
	t.Run("wrong-port", func(t *testing.T) {
		ctx := context.Background()
		opts := &redis.Options{Addr: "127.0.0.1:6378"} // wrong port
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		client := NewClient(opts, WithServiceName("my-redis"))
		_, err := client.Get(ctx, "key").Result()

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]

		assert.Equal("redis.command", span.OperationName())
		assert.NotNil(err)
		assert.Equal(err, span.Tag(ext.Error))
		assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
		assert.Equal("6378", span.Tag(ext.TargetPort))
		assert.Equal("get key: ", span.Tag("redis.raw_command"))
	})

	t.Run("nil", func(t *testing.T) {
		ctx := context.Background()
		opts := &redis.Options{Addr: "127.0.0.1:6379"}
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		client := NewClient(opts, WithServiceName("my-redis"))
		_, err := client.Get(ctx, "non_existent_key").Result()

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]

		assert.Equal(redis.Nil, err)
		assert.Equal("redis.command", span.OperationName())
		assert.Empty(span.Tag(ext.Error))
		assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
		assert.Equal("6379", span.Tag(ext.TargetPort))
		assert.Equal("get non_existent_key: ", span.Tag("redis.raw_command"))
	})
}
func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...ClientOption) {
		ctx := context.Background()
		client := NewClient(&redis.Options{Addr: "127.0.0.1:6379"}, opts...)
		client.Set(ctx, "test_key", "test_value", 0)
		pipeline := client.Pipeline()
		pipeline.Expire(ctx, "pipeline_counter", time.Hour)
		pipeline.Exec(ctx)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)
		for _, s := range spans {
			assert.Equal(t, rate, s.Tag(ext.EventSampleRate))
		}
	}

	t.Run("defaults", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil)
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.4)
	})

	t.Run("enabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 1.0, WithAnalytics(true))
	})

	t.Run("disabled", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, nil, WithAnalytics(false))
	})

	t.Run("override", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		assertRate(t, mt, 0.23, WithAnalyticsRate(0.23))
	})

	t.Run("zero", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		assertRate(t, mt, 0.0, WithAnalyticsRate(0.0))
	})
}

func TestWithContext(t *testing.T) {
	opts := &redis.Options{Addr: "127.0.0.1:6379"}
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	ctx1 := context.Background()
	ctx2 := context.Background()
	client1 := NewClient(opts, WithServiceName("my-redis"))
	s1, ctx1 := tracer.StartSpanFromContext(ctx1, "span1.name")

	s2, ctx2 := tracer.StartSpanFromContext(ctx2, "span2.name")
	client2 := NewClient(opts, WithServiceName("my-redis"))
	client1.Set(ctx1, "test_key", "test_value", 0)
	client2.Get(ctx2, "test_key")
	s1.Finish()
	s2.Finish()

	spans := mt.FinishedSpans()
	assert.Len(spans, 4)
	var span1, span2, setSpan, getSpan mocktracer.Span
	for _, s := range spans {
		switch s.Tag(ext.ResourceName) {
		case "span1.name":
			span1 = s
		case "span2.name":
			span2 = s
		case "set":
			setSpan = s
		case "get":
			getSpan = s
		}
	}

	assert.NotNil(span1)
	assert.NotNil(span2)
	assert.NotNil(setSpan)
	assert.NotNil(getSpan)
	assert.Equal(span1.SpanID(), setSpan.ParentID())
	assert.Equal(span2.SpanID(), getSpan.ParentID())
}
