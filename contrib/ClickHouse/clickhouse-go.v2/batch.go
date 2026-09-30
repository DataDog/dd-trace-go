// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package clickhouse

import (
	"context"
	"errors"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// tracedBatch wraps the batch returned by tracedConn.PrepareBatch. Callers use it
// as a driver.Batch: embedded methods such as Append go straight to the driver,
// while Send, Flush, and Abort produce separate spans. Those spans use the
// context, query, and tracing options captured when the batch was prepared.
type tracedBatch struct {
	driver.Batch
	cfg   *config
	query string
	ctx   context.Context
}

// Send traces the batch send using the context passed to PrepareBatch.
func (b *tracedBatch) Send() error {
	start := time.Now()
	err := b.Batch.Send()
	trace(b.ctx, b.cfg, "BatchSend", b.query, start, err)
	return err
}

// Flush traces a batch flush using the context passed to PrepareBatch.
func (b *tracedBatch) Flush() error {
	start := time.Now()
	err := b.Batch.Flush()
	trace(b.ctx, b.cfg, "BatchFlush", b.query, start, err)
	return err
}

// Abort traces a batch abort unless the driver returns ErrBatchAlreadySent,
// which indicates cleanup after Send rather than another database operation.
func (b *tracedBatch) Abort() error {
	start := time.Now()
	err := b.Batch.Abort()
	// A deferred Abort after Send is cleanup, not a database operation.
	if errors.Is(err, clickhouse.ErrBatchAlreadySent) {
		return err
	}
	trace(b.ctx, b.cfg, "BatchAbort", b.query, start, err)
	return err
}
