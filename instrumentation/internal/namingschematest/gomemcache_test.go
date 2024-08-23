// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memcachetrace "github.com/DataDog/dd-trace-go/contrib/bradfitz/gomemcache/v2/memcache"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var gomemcache = harness.TestCase{
	Name: instrumentation.PackageBradfitzGoMemcache,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		li := startMemcacheTestServer(t)
		defer li.Close()
		addr := li.Addr().String()

		var opts []memcachetrace.ClientOption
		if serviceOverride != "" {
			opts = append(opts, memcachetrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		client := memcachetrace.WrapClient(memcache.New(addr), opts...)
		client.Timeout = 2 * time.Second // Default timeout is 100ms, it can be short for the CI runner.

		defer client.DeleteAll()
		err := client.Add(&memcache.Item{Key: "key1", Value: []byte("value1")})
		require.NoError(t, err)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		return spans
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"memcached"},
		DDService:       []string{"memcached"},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "memcached.query", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "memcached.command", spans[0].OperationName())
	},
}

func startMemcacheTestServer(t *testing.T) net.Listener {
	li, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

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
