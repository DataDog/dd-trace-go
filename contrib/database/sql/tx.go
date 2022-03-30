// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"context"
	"database/sql/driver"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"time"
)

var _ driver.Tx = (*tracedTx)(nil)

// tracedTx is a traced version of sql.Tx
type tracedTx struct {
	driver.Tx
	*traceParams
	ctx context.Context
}

// Commit sends a span at the end of the transaction
func (t *tracedTx) Commit() (err error) {
	start := time.Now()
	span := t.tryStartTrace(t.ctx, queryTypeCommit, "", start, &tracer.SQLCommentCarrier{}, err)
	if span != nil {
		go func() {
			span.Finish(tracer.WithError(err))
		}()
	}
	err = t.Tx.Commit()
	return err
}

// Rollback sends a span if the connection is aborted
func (t *tracedTx) Rollback() (err error) {
	start := time.Now()
	span := t.tryStartTrace(t.ctx, queryTypeRollback, "", start, &tracer.SQLCommentCarrier{}, err)
	err = t.Tx.Rollback()
	if span != nil {
		go func() {
			span.Finish(tracer.WithError(err))
		}()
	}
	return err
}
