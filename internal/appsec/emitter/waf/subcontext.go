// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package waf

import (
	"context"

	"github.com/DataDog/go-libddwaf/v5"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/dyngo"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/waf/addresses"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

type SubcontextOperation struct {
	contextOp  *ContextOperation
	subcontext *libddwaf.Subcontext
}

func (op *ContextOperation) NewSubcontextOp() *SubcontextOperation {
	ctx := op.context.Load()
	if ctx == nil {
		log.Debug("appsec: WAF context is closed, skipping WAF subcontext creation")
		return &SubcontextOperation{contextOp: op}
	}

	subcontext, err := ctx.NewSubcontext(context.Background())
	if err != nil {
		log.Debug("appsec: failed to create WAF subcontext: %s", err.Error())
		return &SubcontextOperation{contextOp: op}
	}

	return &SubcontextOperation{contextOp: op, subcontext: subcontext}
}

func (sub *SubcontextOperation) Run(eventReceiver dyngo.Operation, addrs addresses.RunAddressData) {
	if sub.subcontext == nil {
		sub.contextOp.skipRASPRuleAfterRequest(addrs)
		return
	}

	sub.contextOp.runWAF(eventReceiver, sub.subcontext, addrs)
}

func (sub *SubcontextOperation) Close() {
	if sub.subcontext == nil {
		return
	}

	sub.subcontext.Close()
	sub.subcontext = nil
}
