// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package clickhouse_test

import (
	"context"
	"log"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	clickhousetrace "github.com/DataDog/dd-trace-go/contrib/ClickHouse/clickhouse-go.v2/v2"
)

func ExampleWrap() {
	if err := tracer.Start(); err != nil {
		log.Print(err)
		return
	}
	defer tracer.Stop()

	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{"localhost:9000"}})
	if err != nil {
		log.Print(err)
		return
	}
	conn = clickhousetrace.Wrap(conn, clickhousetrace.WithService("analytics-api"))
	defer conn.Close()

	var version string
	if err := conn.QueryRow(context.Background(), "SELECT version()").Scan(&version); err != nil {
		log.Print(err)
	}
}
