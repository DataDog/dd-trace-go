// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.
// Portions Copyright (c) 2026 CloudX. See LICENSE for the original MIT license.

// Package clickhouse adds Datadog spans to the github.com/ClickHouse/clickhouse-go/v2
// connection API (v2.48.0 and later). Both native and HTTP transports are supported.
// Start the tracer in your application, then wrap a connection:
//
//	conn, err := clickhouse.Open(&clickhouse.Options{Addr: []string{"localhost:9000"}})
//	if err != nil {
//	    return err
//	}
//	conn = clickhousetrace.Wrap(conn)
//	defer conn.Close()
//	err = conn.Ping(ctx)
//
// Pass a context with an active span to make the ClickHouse span its child.
// The wrapper returns driver errors unchanged. A span covers its driver method
// call, not later row scans or reads. This package does not instrument
// database/sql connections.
package clickhouse
