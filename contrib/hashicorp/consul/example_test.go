package consul

import (
	"context"
	"fmt"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	consul "github.com/hashicorp/consul/api"
)

func Example() {
	// Get a new Consul client
	client, err := NewClient(consul.DefaultConfig())
	if err != nil {
		panic(err)
	}

	// Optionally, create a new root span
	root, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.SpanType(ext.SpanTypeConsul),
		tracer.ServiceName("consul.example"),
		tracer.ResourceName("/home"),
	)
	defer root.Finish()
	client = client.WithContext(ctx)

	// Get a handle to the KV API
	kv := client.KV()

	// PUT a new KV pair
	p := &consul.KVPair{Key: "test", Value: []byte("1000")}
	_, err = kv.Put(p, nil)
	if err != nil {
		panic(err)
	}

	// Lookup the pair
	pair, _, err := kv.Get("test", nil)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%v: %s\n", pair.Key, pair.Value)
	// Output:
	// test: 1000
}
