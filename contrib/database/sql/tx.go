package sql

import (
	"context"
	"database/sql/driver"

	"github.com/DataDog/dd-trace-go/ddtrace/tracer"
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
	span := t.newChildSpanFromContext(t.ctx, "Commit", "")
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return t.Tx.Commit()
}

// Rollback sends a span if the connection is aborted
func (t *tracedTx) Rollback() (err error) {
	span := t.newChildSpanFromContext(t.ctx, "Rollback", "")
	defer func() {
		span.Finish(tracer.WithError(err))
	}()
	return t.Tx.Rollback()
}
