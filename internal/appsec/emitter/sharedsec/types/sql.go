package types

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
)

type (
	SQLExecOperation struct {
		dyngo.Operation
	}
	SQLPrepareOperation struct {
		dyngo.Operation
	}
	SQLQueryOperation struct {
		dyngo.Operation
	}

	SQLExecOperationArgs struct {
		Query string
	}
	SQLExecOperationRes struct{}

	SQLPrepareOperationArgs struct {
		Query string
	}
	SQLPrepareOperationRes struct{}

	SQLQueryOperationArgs struct {
		Query string
	}
	SQLQueryOperationRes struct{}
)

func (SQLExecOperationArgs) IsArgOf(*SQLExecOperation)   {}
func (SQLExecOperationRes) IsResultOf(*SQLExecOperation) {}

func (SQLPrepareOperationArgs) IsArgOf(*SQLPrepareOperation)   {}
func (SQLPrepareOperationRes) IsResultOf(*SQLPrepareOperation) {}

func (SQLQueryOperationArgs) IsArgOf(*SQLQueryOperation)   {}
func (SQLQueryOperationRes) IsResultOf(*SQLQueryOperation) {}
