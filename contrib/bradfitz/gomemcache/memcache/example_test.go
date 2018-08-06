package memcache_test

import (
	"context"

	"github.com/bradfitz/gomemcache/memcache"
	memcachetrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/bradfitz/gomemcache/memcache"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func Example() {
	span, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)

	mc := memcachetrace.WrapClient(memcache.New("127.0.0.1:11211"))
	// you can use WithContext to set the parent span
	mc.WithContext(ctx).Set(&memcache.Item{Key: "my key", Value: []byte("my value")})

	span.Finish()
}
