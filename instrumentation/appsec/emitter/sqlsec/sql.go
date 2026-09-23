// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqlsec

import (
	"context"
	"sync"

	"github.com/DataDog/dd-trace-go/v2/appsec/events"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

var badInputContextOnce sync.Once

type (
	SQLOperation struct {
		dyngo.Operation
	}

	SQLOperationArgs struct {
		// Query corresponds to the addres `server.db.statement`
		Query string
		// Driver corresponds to the addres `server.db.system`
		Driver string
		// MonitorOnly reports attacks without applying blocking or redirect actions.
		MonitorOnly bool
	}
	SQLOperationRes struct{}
)

func (SQLOperationArgs) IsArgOf(*SQLOperation)   {}
func (SQLOperationRes) IsResultOf(*SQLOperation) {}

func ProtectSQLOperation(ctx context.Context, query, driver string) error {
	return emitSQLOperation(ctx, query, driver, false)
}

type monitoringDisabledKey struct{}

// WithSQLMonitoringDisabled marks a driver call already checked by an outer SQL
// integration. Only pass the returned context to that call, not subsequent queries.
func WithSQLMonitoringDisabled(ctx context.Context) context.Context {
	return context.WithValue(ctx, monitoringDisabledKey{}, true)
}

// MonitorSQLOperation reports SQL injection attempts without interrupting execution.
// An outer SQL integration can suppress duplicate monitoring with
// WithSQLMonitoringDisabled while retaining its own blocking behavior.
func MonitorSQLOperation(ctx context.Context, query, driver string) {
	if disabled, _ := ctx.Value(monitoringDisabledKey{}).(bool); disabled {
		return
	}
	_ = emitSQLOperation(ctx, query, driver, true)
}

func emitSQLOperation(ctx context.Context, query, driver string, monitorOnly bool) error {
	opArgs := SQLOperationArgs{
		Query:       query,
		Driver:      driver,
		MonitorOnly: monitorOnly,
	}

	parent, _ := dyngo.FromContext(ctx)
	if parent == nil { // No parent operation => we can't monitor the request
		badInputContextOnce.Do(func() {
			log.Debug("appsec: outgoing SQL operation monitoring ignored: could not find the handler " +
				"instrumentation metadata in the request context: the request handler is not being monitored by a " +
				"middleware function or the incoming request context has not be forwarded correctly to the SQL connection")
		})
		return nil
	}

	op := &SQLOperation{
		Operation: dyngo.NewOperation(parent),
	}

	var err *events.BlockingSecurityEvent
	// TODO: move the data listener as a setup function of SQLsec.StartSQLOperation(ars, <setup>)
	if !monitorOnly {
		dyngo.OnData(op, func(e *events.BlockingSecurityEvent) {
			err = e
		})
	}

	dyngo.StartOperation(op, opArgs)
	dyngo.FinishOperation(op, SQLOperationRes{})

	if err != nil {
		log.Debug("appsec: outgoing SQL operation blocked by the WAF")
		return err
	}

	return nil
}
