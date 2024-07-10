// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sqlsec

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/sqlsec/types"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/listener/sharedsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/trace"

	"github.com/DataDog/appsec-internal-go/limiter"
	waf "github.com/DataDog/go-libddwaf/v3"
)

const (
	ServerDBStatementAddr = "server.db.statement"
	ServerDBTypeAddr      = "server.db.system"
)

func RegisterSQLListener(op dyngo.Operation, events *trace.SecurityEventsHolder, wafCtx *waf.Context, limiter limiter.Limiter) {
	dyngo.On(op, sharedsec.MakeWAFRunListener(events, wafCtx, limiter, func(args types.SQLOperationArgs) waf.RunAddressData {
		return waf.RunAddressData{Ephemeral: map[string]any{ServerDBStatementAddr: args.Query, ServerDBTypeAddr: args.Driver}}
	}))
}

func SQLAddressesPresent(addresses listener.AddressSet) bool {
	_, queryAddr := addresses[ServerDBStatementAddr]
	_, driverAddr := addresses[ServerDBTypeAddr]

	return queryAddr || driverAddr

}
