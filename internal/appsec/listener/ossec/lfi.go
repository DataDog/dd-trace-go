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
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

const (
	ServerIOFSFileAddr = "server.io.fs.file"
)

func RegisterOpenListener(op dyngo.Operation, eventsHolder *trace.SecurityEventsHolder, wafCtx *waf.Context, limiter limiter.Limiter) {
	println("RegisterOpenListener")
	runWAF := sharedsec.MakeWAFRunListener(eventsHolder, wafCtx, limiter, func(args ossec.OpenOperationArgs) waf.RunAddressData {
		return waf.RunAddressData{Ephemeral: map[string]any{ServerIOFSFileAddr: args.Path}}
	})

	dyngo.On(op, func(op *ossec.OpenOperation, args ossec.OpenOperationArgs) {
		// We only care about read operations. We don't want to scan for write operations
		// os.O_RDONLY is not a flag it's simply 0, so we can't do a simple bit mask
		// If os.O_CREATE is set, we also want to monitor the operation because the file must not exist
		if (args.Flags&os.O_RDWR == 0 && args.Flags != os.O_RDONLY) || args.Flags&os.O_CREATE != 0 {
			return
		}

		dyngo.OnData(op, func(e *events.BlockingSecurityEvent) {
			println("dyngo.OnData")
			dyngo.OnFinish(op, func(op *ossec.OpenOperation, res ossec.OpenOperationRes) {
				println("dyngo.OnFinish")
				if res.Err != nil {
					*res.Err = e
				}
			})
		})

		println("runWAF")
		runWAF(op, args)
	})
}
