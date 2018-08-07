package leveldb_test

import (
	"context"

	leveldbtrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/syndtr/goleveldb/leveldb"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func Example() {
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
