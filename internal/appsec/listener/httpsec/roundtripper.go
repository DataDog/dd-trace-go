// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/DataDog/appsec-internal-go/limiter"
	"github.com/DataDog/go-libddwaf/v3"
)

// RegisterRoundTripperListener registers a listener on outgoing HTTP client requests to run the WAF.
func RegisterRoundTripperListener(op dyngo.Operation, events *trace.SecurityEventsHolder, wafCtx *waf.Context, limiter limiter.Limiter) {
	dyngo.On(op, func(op *types.RoundTripOperation, args types.RoundTripOperationArgs) {
		wafResult := sharedsec.RunWAF(wafCtx, waf.RunAddressData{Persistent: map[string]any{ServerIoNetURLAddr: args.URL}})
		if !wafResult.HasEvents() {
			return
		}

		log.Debug("appsec: WAF detected a suspicious outgoing request URL: %s", args.URL)

		sharedsec.ProcessActions(op, wafResult.Actions)
		sharedsec.AddSecurityEvents(events, limiter, wafResult.Events)
	})
}
