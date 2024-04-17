package sql

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sharedsec/types"
)

func SQLOperation(qType QueryType, query string) {
	switch qType {
	case QueryTypeExec:
		dyngo.StartOperation(&types.SQLExecOperation{}, types.SQLExecOperationArgs{Query: query})
	case QueryTypePrepare:
		dyngo.StartOperation(&types.SQLPrepareOperation{}, types.SQLPrepareOperationArgs{Query: query})
	case QueryTypeQuery:
		dyngo.StartOperation(&types.SQLQueryOperation{}, types.SQLQueryOperationArgs{Query: query})
	}
}
