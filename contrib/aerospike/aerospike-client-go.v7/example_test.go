// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aerospike_test

import (
	"context"
	"log"

	as "github.com/aerospike/aerospike-client-go/v7"

	astrace "github.com/DataDog/dd-trace-go/contrib/aerospike/aerospike-client-go.v7/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
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
	if err = ac.WithContext(ctx).Put(nil, key, as.BinMap{"value": 3}); err != nil {
		log.Fatal(err)
	}
}
