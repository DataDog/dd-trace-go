// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package buntdb_test

import (
	"context"
	"log"

	buntdbtrace "github.com/DataDog/dd-trace-go/contrib/tidwall/buntdb/v2"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func Example() {
	tracer.Start()
	defer tracer.Stop()

	db, err := buntdbtrace.Open(":memory:")
	if err != nil {
		log.Fatal(err)
	}

	// Create a root span, giving name, server and resource.
	_, ctx := tracer.StartSpanFromContext(context.Background(), "my-query",
		tracer.ServiceName("my-db"),
		tracer.ResourceName("initial-access"),
	)

	// use WithContext to associate the span with the parent
	db.WithContext(ctx).
		Update(func(tx *buntdbtrace.Tx) error {
			_, _, err := tx.Set("key", "value", nil)
			return err
		})

	db.View(func(tx *buntdbtrace.Tx) error {
		// you can also use WithContext on the transaction
		val, err := tx.WithContext(ctx).Get("key")
		log.Println(val)
		return err
	})
}
