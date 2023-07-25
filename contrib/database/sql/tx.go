// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"context"
	"database/sql/driver"
	"runtime/trace"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

var _ driver.Tx = (*tracedTx)(nil)

// tracedTx is a traced version of sql.Tx
type tracedTx struct {
	driver.Tx
	*traceParams
	ctx context.Context
}

func noopTaskEnd() {}

// startTraceTask creates an execution trace task with the given name, and
// returns a context.Context associated with the task, and a function to end the
// task.
//
// This is intended for cases where a span would normally be created after an
// operation, where the operation may have been skipped and a span would be
// noisy. Execution trace tasks must cover the actual duration of the operation
// and can't be altered after the fact.
func startTraceTask(ctx context.Context, name string) (context.Context, func()) {
	if !trace.IsEnabled() {
		return ctx, noopTaskEnd
	}
	ctx, task := trace.NewTask(ctx, name)
	return internal.WithExecutionTraced(ctx), task.End
}

// Commit sends a span at the end of the transaction
func (t *tracedTx) Commit() (err error) {
	ctx, end := startTraceTask(t.ctx, QueryTypeCommit)
	defer end()

	start := time.Now()
	err = t.Tx.Commit()
	t.tryTrace(ctx, QueryTypeCommit, "", start, err)
	return err
}

// Rollback sends a span if the connection is aborted
func (t *tracedTx) Rollback() (err error) {
	ctx, end := startTraceTask(t.ctx, QueryTypeRollback)
	defer end()

	start := time.Now()
	err = t.Tx.Rollback()
	t.tryTrace(ctx, QueryTypeRollback, "", start, err)
	return err
}
