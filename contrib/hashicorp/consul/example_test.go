// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package consul_test

import (
	"context"
	"fmt"
	"log"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	consul "github.com/hashicorp/consul/api"

	ddconsul "github.com/DataDog/dd-trace-go/contrib/hashicorp/consul/v2"
)

// Here's an example illustrating a simple use case for interacting with consul with tracing enabled.
func Example() {
	tracer.Start()
	defer tracer.Stop()

	// Get a new Consul client
	client, err := ddconsul.NewClient(consul.DefaultConfig(), ddconsul.WithService("consul.example"))
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
