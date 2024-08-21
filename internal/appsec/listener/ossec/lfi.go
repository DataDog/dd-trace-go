// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package ossec

import (
	"os"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v3"

	"gopkg.in/DataDog/dd-trace-go.v1/appsec/events"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/ossec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

const (
	ServerIOFSFileAddr = "server.io.fs.file"
)

func RegisterOpenListener(op dyngo.Operation, eventsHolder *trace.SecurityEventsHolder, wafCtx *waf.Context, limiter limiter.Limiter) {
	runWAF := sharedsec.MakeWAFRunListener(eventsHolder, wafCtx, limiter, func(args ossec.OpenOperationArgs) waf.RunAddressData {
		return waf.RunAddressData{Ephemeral: map[string]any{ServerIOFSFileAddr: args.Path}}
	})

	dyngo.On(op, func(op *ossec.OpenOperation, args ossec.OpenOperationArgs) {
		dyngo.OnData(op, func(e *events.BlockingSecurityEvent) {
			dyngo.OnFinish(op, func(_ *ossec.OpenOperation, res ossec.OpenOperationRes[*os.File]) {
				if res.Err != nil {
					*res.Err = e
				}
			})
		})
		runWAF(op, args)
	})
}

func OSAddressesPresent(addresses listener.AddressSet) bool {
	_, fileAddr := addresses[ServerIOFSFileAddr]
	return fileAddr
}
