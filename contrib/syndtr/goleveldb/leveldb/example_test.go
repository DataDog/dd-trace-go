// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package leveldb_test

import (
	"context"

	leveldbtrace "github.com/DataDog/dd-trace-go/contrib/syndtr/goleveldb/v2/leveldb"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func Example() {
	tracer.Start()
	defer tracer.Stop()

	db, _ := leveldbtrace.OpenFile("/tmp/example.leveldb", nil)

	// Create a root span, giving name, server and resource.
	_, ctx := tracer.StartSpanFromContext(context.Background(), "my-query",
		tracer.ServiceName("my-db"),
		tracer.ResourceName("initial-access"),
	)

	// use WithContext to associate the span with the parent
	db.WithContext(ctx).
		// calls will be traced
		Put([]byte("key"), []byte("value"), nil)
}
