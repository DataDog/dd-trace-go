// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestSkipRaw(t *testing.T) {
	runCmds := func(t *testing.T, opts ...ClientOption) []mocktracer.Span {
		mt := mocktracer.Start()
		defer mt.Stop()
		ctx := context.Background()
		client := NewClient(&redis.Options{Addr: "127.0.0.1:6379"}, opts...)
		client.Set(ctx, "test_key", "test_value", 0)
		pipeline := client.Pipeline()
		pipeline.Expire(ctx, "pipeline_counter", time.Hour)
		pipeline.Exec(ctx)
		spans := mt.FinishedSpans()
		assert.Len(t, spans, 3) // dial + set + redis.pipeline
		var setSpan, expireSpan mocktracer.Span
		for _, s := range spans {
			// pick up the spans except dial
			switch s.Tag(ext.ResourceName) {
			case "set":
				setSpan = s
			case "redis.pipeline":
				expireSpan = s
			}
		}
		assert.NotNil(t, setSpan)
		assert.NotNil(t, expireSpan)
		return []mocktracer.Span{setSpan, expireSpan}
	}

	t.Run("true", func(t *testing.T) {
		spans := runCmds(t, WithSkipRawCommand(true))
		for _, span := range spans {
			raw, ok := span.Tags()["redis.raw_command"]
			assert.False(t, ok)
			assert.Empty(t, raw)
		}
	})

	t.Run("default", func(t *testing.T) {
		spans := runCmds(t)
		raw, ok := spans[0].Tags()["redis.raw_command"]
		assert.True(t, ok)
		assert.Equal(t, "set test_key test_value: ", raw)
		raw, ok = spans[1].Tags()["redis.raw_command"]
		assert.True(t, ok)
		assert.Equal(t, "expire pipeline_counter 3600: false", raw)
	})

	t.Run("false", func(t *testing.T) {
		spans := runCmds(t, WithSkipRawCommand(false))
		raw, ok := spans[0].Tags()["redis.raw_command"]
		assert.True(t, ok)
		assert.Equal(t, "set test_key test_value: ", raw)
		raw, ok = spans[1].Tags()["redis.raw_command"]
		assert.True(t, ok)
		assert.Equal(t, "expire pipeline_counter 3600: false", raw)
	})
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
	require.Len(t, spans, 1)

	span := spans[0]
	assert.Equal("redis.command", span.OperationName())
	assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
	assert.Equal("my-redis", span.Tag(ext.ServiceName))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("evalsha", span.Tag(ext.ResourceName))
	assert.Equal("redis/go-redis.v9", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
	assert.Equal("redis", span.Tag(ext.DBSystem))
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
	assert.Len(spans, 2) // dial + command
	var span mocktracer.Span
	for _, s := range spans {
		// pick up the redis.command span except dial
		if s.OperationName() == "redis.command" {
			span = s
		}
	}
	assert.NotNil(span)
	assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
	assert.Equal("my-redis", span.Tag(ext.ServiceName))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("set test_key test_value: ", span.Tag("redis.raw_command"))
	assert.Equal("3", span.Tag("redis.args_length"))
	assert.Equal("redis/go-redis.v9", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
	assert.Equal("redis", span.Tag(ext.DBSystem))
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
			if tc.name == "cluster-client" {
				assert.Len(spans, 1) // only the command span, dial span is not included
			} else {
				assert.Len(spans, 2)
			}

			var span mocktracer.Span
			for _, s := range spans {
				// pick up the redis.command span except dial
				if s.OperationName() == "redis.command" {
					span = s
				}
			}
			assert.NotNil(span)
			assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
			assert.Equal("my-redis", span.Tag(ext.ServiceName))
			assert.Equal("set test_key test_value: ", span.Tag("redis.raw_command"))
			assert.Equal("3", span.Tag("redis.args_length"))
			assert.Equal("redis/go-redis.v9", span.Tag(ext.Component))
			assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
			assert.Equal("redis", span.Tag(ext.DBSystem))
		})
	}
}

func TestAdditionalTagsFromClient(t *testing.T) {
	t.Run("simple-client", func(t *testing.T) {
		simpleClientOpts := &redis.UniversalOptions{Addrs: []string{"127.0.0.1:6379"}}
		simpleClient := redis.NewUniversalClient(simpleClientOpts)
		config := &ddtrace.StartSpanConfig{}
		expectedTags := map[string]interface{}{
			"component": "redis/go-redis.v9",
			"db.system": "redis",
			"out.db":    "0",
			"out.host":  "127.0.0.1",
			"out.port":  "6379",
			"span.kind": "client",
			"span.type": "redis",
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
			"out.db":    "0",
			"component": "redis/go-redis.v9",
			"db.system": "redis",
			"span.kind": "client",
			"span.type": "redis",
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
			"addrs":     "127.0.0.1:6379, 127.0.0.2:6379",
			"component": "redis/go-redis.v9",
			"db.system": "redis",
			"span.kind": "client",
			"span.type": "redis",
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
	require := require.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := NewClient(opts, WithServiceName("my-redis"))
	pipeline := client.Pipeline()
	pipeline.Expire(ctx, "pipeline_counter", time.Hour)

	// Exec with context test
	pipeline.Exec(ctx)

	spans := mt.FinishedSpans()
	var span mocktracer.Span
	for _, s := range spans {
		// pick up the redis.command span except dial
		if s.OperationName() == "redis.command" {
			span = s
		}
	}
	assert.NotNil(span)
	assert.Equal("redis.command", span.OperationName())
	assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
	assert.Equal("my-redis", span.Tag(ext.ServiceName))
	assert.Equal("redis.pipeline", span.Tag(ext.ResourceName))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("1", span.Tag("redis.pipeline_length"))
	assert.Equal("redis/go-redis.v9", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
	assert.Equal("redis", span.Tag(ext.DBSystem))

	mt.Reset()
	pipeline.Expire(ctx, "pipeline_counter", time.Hour)
	pipeline.Expire(ctx, "pipeline_counter_1", time.Minute)

	// Rewriting Exec
	pipeline.Exec(ctx)

	spans = mt.FinishedSpans()
	require.Len(spans, 1)

	span = spans[0]
	assert.Equal("redis.command", span.OperationName())
	assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
	assert.Equal("my-redis", span.Tag(ext.ServiceName))
	assert.Equal("redis.pipeline", span.Tag(ext.ResourceName))
	assert.Equal("2", span.Tag("redis.pipeline_length"))
	assert.Equal("redis/go-redis.v9", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
	assert.Equal("redis", span.Tag(ext.DBSystem))
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
	assert.Len(spans, 3) // child + parent + dial
	var child, parent, dial mocktracer.Span
	for _, s := range spans {
		// order of traces in buffer is not guaranteed
		switch s.OperationName() {
		case "redis.command":
			child = s
		case "parent.span":
			parent = s
		case "redis.dial":
			dial = s
		}
	}
	assert.NotNil(parent)
	assert.NotNil(child)
	assert.NotNil(dial)

	assert.Equal(child.ParentID(), parent.SpanID())
	assert.Equal(child.Tag(ext.TargetHost), "127.0.0.1")
	assert.Equal(child.Tag(ext.TargetPort), "6379")
	assert.Equal(dial.ParentID(), child.SpanID())
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

	// Checking all commands were recorded
	commands := make([]string, 0, 4)
	for _, s := range spans {
		// pick up the redis.command span except dial
		if s.OperationName() == "redis.command" {
			commands = append(commands, s.Tag("redis.raw_command").(string))
		}
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
		assert.Len(spans, 1+opts.MaxRetries+1) // 1 dial + dial MaxRetries + redis.command
		var span mocktracer.Span
		for _, s := range spans {
			// pick up the redis.command span except dial
			if s.OperationName() == "redis.command" {
				span = s
			}
		}
		assert.Equal("redis.command", span.OperationName())
		assert.NotNil(err)
		assert.Equal(err, span.Tag(ext.Error))
		assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
		assert.Equal("6378", span.Tag(ext.TargetPort))
		assert.Equal("get key: ", span.Tag("redis.raw_command"))
		assert.Equal("redis/go-redis.v9", span.Tag(ext.Component))
		assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
		assert.Equal("redis", span.Tag(ext.DBSystem))
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
		var span mocktracer.Span
		for _, s := range spans {
			// pick up the redis.command span except dial
			if s.OperationName() == "redis.command" {
				span = s
			}
		}
		assert.Equal(redis.Nil, err)
		assert.Equal("redis.command", span.OperationName())
		assert.Empty(span.Tag(ext.Error))
		assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
		assert.Equal("6379", span.Tag(ext.TargetPort))
		assert.Equal("get non_existent_key: ", span.Tag("redis.raw_command"))
		assert.Equal("redis/go-redis.v9", span.Tag(ext.Component))
		assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
		assert.Equal("redis", span.Tag(ext.DBSystem))
	})

	t.Run("pipeline wrong-port", func(t *testing.T) {
		ctx := context.Background()
		opts := &redis.Options{Addr: "127.0.0.1:6378"} // wrong port
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		client := NewClient(opts, WithServiceName("my-redis"))
		pipeline := client.Pipeline()
		pipeline.Get(ctx, "key")

		// Exec with context test
		_, err := pipeline.Exec(ctx)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1+opts.MaxRetries+1) // 1 dial + dial MaxRetries + redis.pipeline
		var span mocktracer.Span
		for _, s := range spans {
			// pick up the redis.command span except dial
			if s.OperationName() == "redis.command" {
				span = s
			}
		}
		assert.Equal("redis.command", span.OperationName())
		assert.NotNil(err)
		assert.Equal(err, span.Tag(ext.Error))
		assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
		assert.Equal("my-redis", span.Tag(ext.ServiceName))
		assert.Equal("redis.pipeline", span.Tag(ext.ResourceName))
		assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
		assert.Equal("6378", span.Tag(ext.TargetPort))
		assert.Equal("1", span.Tag("redis.pipeline_length"))
		assert.Equal("redis/go-redis.v9", span.Tag(ext.Component))
		assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
		assert.Equal("redis", span.Tag(ext.DBSystem))
	})

	t.Run("errcheck", func(t *testing.T) {
		opts := &redis.Options{Addr: "127.0.0.1:6379"}
		assert := assert.New(t)
		mt := mocktracer.Start()
		defer mt.Stop()

		errCheckFn := func(err error) bool {
			return err != nil && !errors.Is(err, context.Canceled)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		client := NewClient(opts, WithServiceName("my-redis"), WithErrorCheck(errCheckFn))
		_, err := client.Get(ctx, "test_key").Result()

		spans := mt.FinishedSpans()
		assert.Len(spans, 1)
		span := spans[0]

		assert.Equal(context.Canceled, err)
		assert.Empty(span.Tag(ext.Error))
		assert.Equal("redis.command", span.OperationName())
		assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
		assert.Equal("6379", span.Tag(ext.TargetPort))
		assert.Equal("get test_key: ", span.Tag("redis.raw_command"))
		assert.Equal("redis/go-redis.v9", span.Tag(ext.Component))
		assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
		assert.Equal("redis", span.Tag(ext.DBSystem))
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
		assert.Len(t, spans, 3) // dial + set + expire
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
	assert.Len(spans, 6) // 2 dial spans + span1, span2, setSpan, getSpan
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

func TestDial(t *testing.T) {
	ctx := context.Background()
	opts := &redis.Options{Addr: "127.0.0.1:6379"}
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	client := NewClient(opts, WithServiceName("my-redis"))
	client.Ping(ctx)

	spans := mt.FinishedSpans()
	assert.Len(spans, 2) // dial + ping
	var span mocktracer.Span
	for _, s := range spans {
		// pick up the dial span
		if s.OperationName() == "redis.dial" {
			span = s
		}
	}
	assert.NotNil(span)
	assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
	assert.Equal("my-redis", span.Tag(ext.ServiceName))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("redis/go-redis.v9", span.Tag(ext.Component))
	assert.Equal(ext.SpanKindClient, span.Tag(ext.SpanKind))
	assert.Equal("redis", span.Tag(ext.DBSystem))
}

func TestNamingSchema(t *testing.T) {
	genSpans := namingschematest.GenSpansFn(func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []ClientOption
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		client := NewClient(&redis.Options{Addr: "127.0.0.1:6379"}, opts...)
		st := client.Set(context.Background(), "test_key", "test_value", 0)
		require.NoError(t, st.Err())

		spans := mt.FinishedSpans()
		var span mocktracer.Span
		for _, s := range spans {
			// pick up the redis.command span except dial
			if s.OperationName() == "redis.command" {
				span = s
			}
		}
		assert.NotNil(t, span)
		return []mocktracer.Span{span}
	})

	namingschematest.NewRedisTest(genSpans, "redis.client")(t)
}
