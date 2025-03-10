// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025-present Datadog, Inc.

package rueidis_test

import (
	"context"
	"log"

	rueidistrace "github.com/DataDog/dd-trace-go/contrib/redis/rueidis/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/redis/rueidis"
)

// To start tracing Redis, simply create a new client using the library and continue
// using as you normally would.
func Example() {
	tracer.Start()
	defer tracer.Stop()

	c, err := rueidistrace.NewClient(rueidis.ClientOption{
		InitAddress: []string{"localhost:6379"},
	})
	if err != nil {
		log.Fatal(err)
		return
	}

	if err := c.Do(context.Background(), c.B().Set().Key("key").Value("value").Build()).Error(); err != nil {
		log.Fatal(err)
		return
	}
}
