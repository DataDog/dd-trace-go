// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package ossec

import (
	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v3"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/ossec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"
)

const (
	ServerIOFSFileAddr = "server.io.fs.file"
)

func RegisterOpenListener(op dyngo.Operation, events *trace.SecurityEventsHolder, wafCtx *waf.Context, limiter limiter.Limiter) {
	dyngo.On(op, sharedsec.MakeWAFRunListener(events, wafCtx, limiter, func(args types.OpenOperationArgs) waf.RunAddressData {
		return waf.RunAddressData{Ephemeral: map[string]any{ServerIOFSFileAddr: args.Path}}
	}))
}
