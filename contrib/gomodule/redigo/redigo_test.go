// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package redigo

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

func TestMain(m *testing.M) {
	_, ok := os.LookupEnv("INTEGRATION")
	if !ok {
		fmt.Println("--- SKIP: to enable integration test, set the INTEGRATION environment variable")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestClient(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	c, err := Dial("tcp", "127.0.0.1:6379", WithServiceName("my-service"))
	assert.Nil(err)
	c.Do("SET", 1, "truck")

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)

	span := spans[0]
	assert.Equal("redis.command", span.OperationName())
	assert.Equal(ext.SpanTypeRedis, span.Tag(ext.SpanType))
	assert.Equal("my-service", span.Tag(ext.ServiceName))
	assert.Equal("SET", span.Tag(ext.ResourceName))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("SET 1 truck", span.Tag("redis.raw_command"))
	assert.Equal("2", span.Tag("redis.args_length"))
}

func TestCommandError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	c, err := Dial("tcp", "127.0.0.1:6379", WithServiceName("my-service"))
	assert.Nil(err)
	_, err = c.Do("NOT_A_COMMAND", context.Background())
	assert.NotNil(err)

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal(err, span.Tag(ext.Error).(error))
	assert.Equal("redis.command", span.OperationName())
	assert.Equal("my-service", span.Tag(ext.ServiceName))
	assert.Equal("NOT_A_COMMAND", span.Tag(ext.ResourceName))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("NOT_A_COMMAND", span.Tag("redis.raw_command"))
}

func TestConnectionError(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	_, err := Dial("tcp", "127.0.0.1:1000", WithServiceName("redis-service"))

	assert.NotNil(err)
	assert.Contains(err.Error(), "dial tcp 127.0.0.1:1000")
}

func TestInheritance(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	root, ctx := tracer.StartSpanFromContext(context.Background(), "parent.span")
	client, err := Dial("tcp", "127.0.0.1:6379", WithServiceName("redis-service"))
	assert.Nil(err)
	client.Do("SET", "water", "bottle", ctx)
	root.Finish()

	spans := mt.FinishedSpans()
	assert.Len(spans, 2)

	var child, parent mocktracer.Span
	for _, s := range spans {
		switch s.OperationName() {
		case "redis.command":
			child = s
		case "parent.span":
			parent = s
		}
	}
	assert.NotNil(child)
	assert.NotNil(parent)

	assert.Equal(child.ParentID(), parent.SpanID())
	assert.Equal(child.Tag(ext.TargetHost), "127.0.0.1")
	assert.Equal(child.Tag(ext.TargetPort), "6379")
}

type stringifyTest struct{ A, B int }

func (ts stringifyTest) String() string { return fmt.Sprintf("[%d, %d]", ts.A, ts.B) }

func TestCommandsToSring(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	str := stringifyTest{A: 57, B: 8}
	c, err := Dial("tcp", "127.0.0.1:6379", WithServiceName("my-service"))
	assert.Nil(err)
	c.Do("SADD", "testSet", "a", int(0), int32(1), int64(2), str, context.Background())

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]

	assert.Equal("redis.command", span.OperationName())
	assert.Equal("my-service", span.Tag(ext.ServiceName))
	assert.Equal("SADD", span.Tag(ext.ResourceName))
	assert.Equal("127.0.0.1", span.Tag(ext.TargetHost))
	assert.Equal("6379", span.Tag(ext.TargetPort))
	assert.Equal("SADD testSet a 0 1 2 [57, 8]", span.Tag("redis.raw_command"))
}

func TestPool(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	pool := &redis.Pool{
		MaxIdle:     2,
		MaxActive:   3,
		IdleTimeout: 23,
		Wait:        true,
		Dial: func() (redis.Conn, error) {
			return Dial("tcp", "127.0.0.1:6379", WithServiceName("my-service"))
		},
	}

	pc := pool.Get()
	pc.Do("SET", " whiskey", " glass", context.Background())

	spans := mt.FinishedSpans()
	assert.Len(spans, 1)
	span := spans[0]
	assert.Equal(span.Tag("out.network"), "tcp")
}

func TestTracingDialUrl(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	url := "redis://127.0.0.1:6379"
	client, err := DialURL(url, WithServiceName("redis-service"))
	assert.Nil(err)
	client.Do("SET", "ONE", " TWO", context.Background())

	spans := mt.FinishedSpans()
	assert.True(len(spans) > 0)
}

func TestAnalyticsSettings(t *testing.T) {
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...interface{}) {
		c, err := Dial("tcp", "127.0.0.1:6379", opts...)
		assert.Nil(t, err)
		c.Do("SET", 1, "truck")

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)

		assert.Equal(t, rate, spans[0].Tag(ext.EventSampleRate))
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
}

func TestDoWithTimeout(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	url := "redis://127.0.0.1:6379"
	client, err := DialURL(url, WithServiceName("redis-service"))
	assert.Nil(err)
	_, err = redis.DoWithTimeout(client, time.Second, "SET", "ONE", " TWO")
	assert.NoError(err)

	spans := mt.FinishedSpans()
	assert.True(len(spans) > 0)
}
