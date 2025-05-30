// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/database/sql"

import (
	"context"
	"database/sql/driver"

	v2 "github.com/DataDog/dd-trace-go/contrib/database/sql/v2"
)

var _ driver.Conn = (*TracedConn)(nil)

// QueryType represents the different available traced db queries.
type QueryType = v2.QueryType

const (
	// QueryTypeConnect is used for Connect traces.
	QueryTypeConnect QueryType = "Connect"
	// QueryTypeQuery is used for Query traces.
	QueryTypeQuery = "Query"
	// QueryTypePing is used for Ping traces.
	QueryTypePing = "Ping"
	// QueryTypePrepare is used for Prepare traces.
	QueryTypePrepare = "Prepare"
	// QueryTypeExec is used for Exec traces.
	QueryTypeExec = "Exec"
	// QueryTypeBegin is used for Begin traces.
	QueryTypeBegin = "Begin"
	// QueryTypeClose is used for Close traces.
	QueryTypeClose = "Close"
	// QueryTypeCommit is used for Commit traces.
	QueryTypeCommit = "Commit"
	// QueryTypeRollback is used for Rollback traces.
	QueryTypeRollback = "Rollback"
)

// TracedConn holds a traced connection with tracing parameters.
type TracedConn struct {
	v2.TracedConn
}

type contextKey int

const spanTagsKey contextKey = 0 // map[string]string

// WithSpanTags creates a new context containing the given set of tags. They will be added
// to any query created with the returned context.
func WithSpanTags(ctx context.Context, tags map[string]string) context.Context {
	return context.WithValue(ctx, spanTagsKey, tags)
}
