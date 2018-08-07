package memcache

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/stretchr/testify/assert"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
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
	client := WrapClient(memcache.New(addr), WithServiceName("test-memcache"))
	defer client.DeleteAll()

	validateMemcacheSpan := func(t *testing.T, span mocktracer.Span, resourceName string) {
		assert.Equal(t, "test-memcache", span.Tag(ext.ServiceName),
			"service name should be set to test-memcache")
		assert.Equal(t, "memcached.query", span.OperationName(),
			"operation name should be set to memcached.query")
		assert.Equal(t, resourceName, span.Tag(ext.ResourceName),
			"resource name should be set to the memcache command")
	}

	t.Run("traces without context", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		err := client.
			Add(&memcache.Item{
				Key:   "key1",
				Value: []byte("value1"),
			})
		assert.Nil(t, err)

		spans := mt.FinishedSpans()
		assert.Len(t, spans, 1)
		validateMemcacheSpan(t, spans[0], "Add")
	})

	t.Run("traces with context", func(t *testing.T) {
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
		assert.Nil(t, err)

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
