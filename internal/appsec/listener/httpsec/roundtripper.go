// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"

	"github.com/DataDog/appsec-internal-go/limiter"
	"github.com/DataDog/go-libddwaf/v3"
)

// RegisterRoundTripperListener registers a listener on outgoing HTTP client requests to run the WAF.
func RegisterRoundTripperListener(op dyngo.Operation, events *trace.SecurityEventsHolder, wafCtx *waf.Context, limiter limiter.Limiter) {
	dyngo.On(op, sharedsec.MakeWAFRunListener(events, wafCtx, limiter, func(args types.RoundTripOperationArgs) waf.RunAddressData {
		return waf.RunAddressData{Ephemeral: map[string]any{ServerIoNetURLAddr: args.URL}}
	}))
}

func SSRFAddressesPresent(addresses listener.AddressSet) bool {
	_, urlAddr := addresses[ServerIoNetURLAddr]
	return urlAddr
}
