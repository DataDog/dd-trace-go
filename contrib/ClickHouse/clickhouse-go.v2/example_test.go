// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package clickhouse_test

import (
	"context"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	clickhouseTrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/ClickHouse/clickhouse-go.v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func Example() {
	ctx := context.Background()
	span, _ := tracer.StartSpanFromContext(ctx, "parent.request",
		tracer.ServiceName("web"),
		tracer.ResourceName("/home"),
	)
	defer span.Finish()
	options := &clickhouse.Options{Addr: []string{"127.0.0.1:9000"}}
	conn, _ := clickhouse.Open(options)
	db := clickhouseTrace.WrapConnection(conn)

	var result struct {
		Col1 uint8
		Col2 string
		Col3 time.Time
	}
	err := db.QueryRow(`SELECT * FROM example WHERE Col1 = ? AND Col3 = ?`,
		6,
		time.Now().Add(time.Duration(6)*time.Hour),
	).Scan(
		&result.Col1,
		&result.Col2,
		&result.Col3,
	)

	span.Finish(tracer.WithError(err))

}
