// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package waf

import (
	"context"
	"errors"

	waf "github.com/DataDog/go-libddwaf/v3"
	wafErrors "github.com/DataDog/go-libddwaf/v3/errors"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/waf/actions"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
)

func (op *ContextOperation) Run(eventReceiver dyngo.Operation, addrs waf.RunAddressData) {
	ctx := op.context.Load()
	if ctx == nil { // Context was closed concurrently
		return
	}

	result, err := ctx.Run(addrs)
	if errors.Is(err, wafErrors.ErrTimeout) {
		log.Debug("appsec: WAF timeout value reached: %v", err)
	} else if err != nil {
		log.Error("appsec: unexpected WAF error: %v", err)
	}

	op.AddEvents(result.Events...)
	op.AbsorbDerivatives(result.Derivatives)

	actions.SendActionEvents(eventReceiver, result.Actions)

	if result.HasEvents() {
		log.Debug("appsec: WAF detected a suspicious event")
	}
}

// RunSimple runs the WAF with the given address data and returns an error that should be forwarded to the caller
func RunSimple(ctx context.Context, addrs waf.RunAddressData, errorLog string) error {
	parent, _ := dyngo.FromContext(ctx)
	if parent == nil {
		log.Error(errorLog)
		return nil
	}

	var err error
	op := dyngo.NewOperation(parent)
	dyngo.OnData(op, func(e *events.BlockingSecurityEvent) {
		err = e
	})
	dyngo.EmitData(op, RunEvent{
		Operation:      op,
		RunAddressData: addrs,
	})
	return err
}
