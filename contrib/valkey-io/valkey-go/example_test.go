// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package valkey_test

import (
	"context"
	"log"

	valkeytrace "github.com/DataDog/dd-trace-go/contrib/valkey-io/valkey-go/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/valkey-io/valkey-go"
)

// To start tracing Valkey, simply create a new client using the library and continue
// using as you normally would.
func Example() {
	tracer.Start()
	defer tracer.Stop()

	vk, err := valkeytrace.NewClient(valkey.ClientOption{
		InitAddress: []string{"localhost:6379"},
	})
	if err != nil {
		log.Fatal(err)
		return
	}

	if err := vk.Do(context.Background(), vk.B().Set().Key("key").Value("value").Build()).Error(); err != nil {
		log.Fatal(err)
		return
	}
}
