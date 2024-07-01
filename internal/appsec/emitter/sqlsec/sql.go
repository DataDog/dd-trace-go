// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqlsec

import (
	"context"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sqlsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

var badInputContextOnce sync.Once

func ProtectSQLOperation(ctx context.Context, query, driver string) error {
	opArgs := types.SQLOperationArgs{
		Query:  query,
		Driver: driver,
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

	op := &types.SQLOperation{
		Operation: dyngo.NewOperation(parent),
	}

	var err *events.BlockingSecurityEvent
	// TODO: move the data listener as a setup function of SQLsec.StartSQLOperation(ars, <setup>)
	dyngo.OnData(op, func(e *events.BlockingSecurityEvent) {
		err = e
	})

	dyngo.StartOperation(op, opArgs)
	dyngo.FinishOperation(op, types.SQLOperationRes{})

	if err != nil {
		log.Debug("appsec: outgoing SQL operation blocked by the WAF")
		return err
	}

	return nil
}
