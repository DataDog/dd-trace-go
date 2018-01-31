package sql

import (
	"context"
	"database/sql/driver"
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
		span.SetError(err)
		span.Finish()
	}()
	return t.Tx.Commit()
}

// Rollback sends a span if the connection is aborted
func (t *tracedTx) Rollback() (err error) {
	span := t.newChildSpanFromContext(t.ctx, "Rollback", "")
	defer func() {
		span.SetError(err)
		span.Finish()
	}()
	return t.Tx.Rollback()
}
