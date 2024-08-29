// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package wafsec

import (
	"errors"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/wafsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	waf "github.com/DataDog/go-libddwaf/v3"
	wafErrors "github.com/DataDog/go-libddwaf/v3/errors"
)

func OnStart(op *wafsec.WAFContextOperation, args wafsec.HTTPArgs) {
	ctx, err := args.Handle.NewContextWithBudget(args.Timeout)
	if err != nil {
		log.Debug("appsec: failed to create WAF context: %v", err)
	}

	op.Ctx.Store(ctx)

	dyngo.OnData(op, func(data waf.RunAddressData) {
		result, err := ctx.Run(data)
		if errors.Is(err, wafErrors.ErrTimeout) {
			log.Debug("appsec: waf timeout value reached: %v", err)
		} else if err != nil {
			log.Error("appsec: unexpected waf error: %v", err)
		}

		if !result.HasEvents() {
			return
		}

		log.Debug("appsec: WAF detected a suspicious WAF event")
		sharedsec.ProcessActions(op, result.Actions)

		if args.Limiter.Allow() {
			log.Warn("appsec: too many WAF events, stopping further processing")
			return
		}

		op.SecurityEventsHolder.AddSecurityEvents(result.Events)
	})
}

func OnFinish(op *wafsec.WAFContextOperation, _ wafsec.HTTPRes) {
	ctx := op.Ctx.Swap(nil)
	if ctx == nil {
		return
	}

	ctx.Close()
}
