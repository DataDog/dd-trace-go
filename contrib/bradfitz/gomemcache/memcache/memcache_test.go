// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package memcache

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/namingschematest"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemcache(t *testing.T) {
	li := makeFakeServer(t)
	defer li.Close()

	testMemcache(t, li.Addr().String())
}

func TestMemcacheIntegration(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}

	testMemcache(t, "localhost:11211")
}

func testMemcache(t *testing.T, addr string) {
	client := getClient(addr, WithServiceName("test-memcache"))
	defer client.DeleteAll()

	validateMemcacheSpan := func(t *testing.T, span mocktracer.Span, resourceName string) {
		assert.Equal(t, "test-memcache", span.Tag(ext.ServiceName),
			"service name should be set to test-memcache")
		assert.Equal(t, "memcached.query", span.OperationName(),
			"operation name should be set to memcached.query")
		assert.Equal(t, resourceName, span.Tag(ext.ResourceName),
			"resource name should be set to the memcache command")
		assert.Equal(t, "bradfitz/gomemcache/memcache", span.Tag(ext.Component),
			"component should be set to gomemcache")
		assert.Equal(t, ext.SpanKindClient, span.Tag(ext.SpanKind),
			"span.kind should be set to client")
		assert.Equal(t, "memcached", span.Tag(ext.DBSystem),
			"db.system should be set to memcached")
	}

	t.Run("default", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		err := client.
			Add(&memcache.Item{
				Key:   "key1",
				Value: []byte("value1"),
			})
		assert.NoError(t, err)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		validateMemcacheSpan(t, spans[0], "Add")
	})

	t.Run("context", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		ctx := context.Background()
		span, ctx := tracer.StartSpanFromContext(ctx, "parent")

		err := client.
			WithContext(ctx).
			Add(&memcache.Item{
				Key:   "key2",
				Value: []byte("value2"),
			})
		assert.NoError(t, err)

		span.Finish()

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 2)
		validateMemcacheSpan(t, spans[0], "Add")
		assert.Equal(t, span, spans[1])
		assert.Equal(t, spans[1].TraceID(), spans[0].TraceID(),
			"memcache span should be part of the parent trace")
	})
}

func TestFakeServer(t *testing.T) {
	li := makeFakeServer(t)
	defer li.Close()

	conn, err := net.Dial("tcp", li.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "add %s\r\n%s\r\n", "key", "value")
	s := bufio.NewScanner(conn)
	assert.True(t, s.Scan())
	assert.Equal(t, "STORED", s.Text())
}

func TestAnalyticsSettings(t *testing.T) {
	li := makeFakeServer(t)
	defer li.Close()
	addr := li.Addr().String()
	assertRate := func(t *testing.T, mt mocktracer.Tracer, rate interface{}, opts ...ClientOption) {
		client := getClient(addr, opts...)
		defer client.DeleteAll()
		err := client.Add(&memcache.Item{Key: "key1", Value: []byte("value1")})
		assert.NoError(t, err)

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

func TestNamingSchema(t *testing.T) {
	li := makeFakeServer(t)
	defer li.Close()
	addr := li.Addr().String()

	genSpans := func(t *testing.T, serviceOverride string) []mocktracer.Span {
		var opts []ClientOption
		if serviceOverride != "" {
			opts = append(opts, WithServiceName(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		client := getClient(addr, opts...)
		defer client.DeleteAll()
		err := client.Add(&memcache.Item{Key: "key1", Value: []byte("value1")})
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		return spans
	}
	assertV0 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "memcached.query", spans[0].OperationName())
	}
	assertV1 := func(t *testing.T, spans []mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "memcached.command", spans[0].OperationName())
	}
	wantServiceNameV0 := namingschematest.ServiceNameAssertions{
		WithDefaults:             []string{"memcached"},
		WithDDService:            []string{"memcached"},
		WithDDServiceAndOverride: []string{namingschematest.TestServiceOverride},
	}
	t.Run("service name", namingschematest.NewServiceNameTest(genSpans, wantServiceNameV0))
	t.Run("operation name", namingschematest.NewSpanNameTest(genSpans, assertV0, assertV1))
}

func makeFakeServer(t *testing.T) net.Listener {
	li, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		for {
			c, err := li.Accept()
			if err != nil {
				break
			}
			go func() {
				defer c.Close()

				// the memcache textual protocol is line-oriented with each
				// command being space separated:
				//
				//    command1 arg1 arg2
				//    command2 arg1 arg2
				//    ...
				//
				s := bufio.NewScanner(c)
				for s.Scan() {
					args := strings.Split(s.Text(), " ")
					switch args[0] {
					case "add":
						if !s.Scan() {
							return
						}
						fmt.Fprintf(c, "STORED\r\n")
					default:
						fmt.Fprintf(c, "SERVER ERROR unknown command: %v \r\n", args[0])
						return
					}
				}
			}()
		}
	}()

	return li
}

func getClient(addr string, opts ...ClientOption) *Client {
	client := WrapClient(memcache.New(addr), opts...)
	client.Timeout = 2 * time.Second // Default timeout is 100ms, it can be short for the CI runner.
	return client
}
