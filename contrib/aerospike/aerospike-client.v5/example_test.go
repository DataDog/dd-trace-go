// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aerospike_test

import (
	"context"
	"log"

	as "github.com/aerospike/aerospike-client-go/v5"

	astrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/aerospike/aerospike-client.v5"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func Example() {
	span, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)
	defer span.Finish()

	client, err := as.NewClient("127.0.0.1", 3000)
	if err != nil {
		log.Fatal(err)
	}

	ac := astrace.WrapClient(client)
	key, err := as.NewKey("namespace", "clientset", "foo")
	if err != nil {
		log.Fatal(err)
	}
	// you can use WithContext to set the parent span
	err = ac.WithContext(ctx).AddBins(nil, key, as.NewBin("value", 3))
	if err != nil {
		log.Fatal(err)
	}
}
