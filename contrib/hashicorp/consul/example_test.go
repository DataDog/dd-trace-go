// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package consul

import (
	"context"
	"fmt"
	"log"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	consul "github.com/hashicorp/consul/api"
)

// Here's an example illustrating a simple use case for interacting with consul with tracing enabled.
func Example() {
	// Get a new Consul client
	client, err := NewClient(consul.DefaultConfig(), WithServiceName("consul.example"))
	if err != nil {
		log.Fatal(err)
	}

	// Optionally, create a new root span
	root, ctx := tracer.StartSpanFromContext(context.Background(), "root_span",
		tracer.SpanType(ext.SpanTypeConsul),
		tracer.ServiceName("example"),
	)
	defer root.Finish()
	client = client.WithContext(ctx)

	// Get a handle to the KV API
	kv := client.KV()

	// PUT a new KV pair
	p := &consul.KVPair{Key: "test", Value: []byte("1000")}
	_, err = kv.Put(p, nil)
	if err != nil {
		log.Fatal(err)
	}

	// Lookup the pair
	pair, _, err := kv.Get("test", nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%v: %s\n", pair.Key, pair.Value)
	// Output:
	// test: 1000
}
